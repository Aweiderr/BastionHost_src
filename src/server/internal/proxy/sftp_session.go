package proxy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"bastion.local/server/internal/managerclient"
)

// runSFTP 把客户端的 SFTP 子系统代理到已授权资产。
//
// 登录名用现有的 `用户名@资产`，多个账号时写成 `用户名@资产#账号`。
// 不在这条通道上询问口令：SFTP 是二进制协议，目标账号必须由堡垒机托管。
func (s *Server) runSFTP(ctx context.Context, ident *identity, ch ssh.Channel, req *ssh.Request, remoteIP string) {
	replied := false
	fail := func(msg string) {
		if !replied {
			_ = req.Reply(false, nil)
			replied = true
		}
		if !strings.HasSuffix(msg, "\n") {
			msg += "\r\n"
		}
		fmt.Fprint(ch, msg)
	}
	if s == nil || s.client == nil {
		fail("SFTP 未就绪。")
		return
	}
	assets, err := s.client.AccessibleFileAssets(ctx, ident.UserID)
	if err != nil {
		fail("无法获取可传输文件的资产：" + err.Error())
		return
	}
	asset, acc, err := pickSFTPTarget(assets, ident.AssetHint)
	if err != nil {
		fail(err.Error())
		return
	}
	check, err := s.client.CheckGrant(ctx, ident.UserID, asset.AssetID, acc.AccountID)
	if err != nil {
		fail("授权校验失败：" + err.Error())
		return
	}
	if !check.Allowed || !check.CanFile {
		reason := "当前授权不允许文件传输"
		if check.Reason != "" && !check.Allowed {
			reason = check.Reason
		}
		fail(reason)
		return
	}

	if redirect, deny := s.gateNewSession(ctx, ident.UserID, asset.AssetID, "sftp", "ssh_client"); deny != "" {
		fail(deny)
		return
	} else if redirect != nil {
		fail(redirectTextFromMap(redirect))
		return
	}
	sess, err := s.client.StartSession(ctx, map[string]any{
		"user_id": ident.UserID, "username": ident.Username, "display_name": ident.DisplayName,
		"asset_id": asset.AssetID, "account_id": acc.AccountID,
		"protocol": "sftp", "client_type": "ssh_client", "client_ip": remoteIP,
		"proxy_node": s.nodeID(), "grant_id": check.GrantID,
		"grant_source": check.GrantSource, "ticket_id": check.TicketID,
	})
	if err != nil {
		fail("创建会话失败：" + err.Error())
		return
	}
	live := &liveSession{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo,
		UserID: ident.UserID, Username: ident.Username, AssetID: asset.AssetID,
	}
	live.addCloser(ch)
	s.registerLive(live)
	defer s.unregisterLive(sess.SessionID)

	outcome := endConnectFailed
	fileCount := 0
	credMode := "managed"
	defer func() {
		status, reason := s.finishDecision(sess.SessionID, outcome)
		if credMode == "" {
			credMode = "managed"
		}
		payload := map[string]any{
			"status": status, "end_reason": reason,
			"command_count": 0, "block_count": 0, "file_count": fileCount,
			"record_status": recordNone, "credential_mode": credMode,
		}
		uctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.client.FinishSession(uctx, sess.SessionID, payload); err != nil {
			slog.Error("SFTP 会话归档失败", "session_id", sess.SessionID, "err", err)
		}
	}()

	cred, err := s.client.FetchCredential(ctx, ident.UserID, ident.Username,
		asset.AssetID, acc.AccountID, sess.SessionNo, remoteIP)
	if err != nil {
		fail("获取连接凭据失败：" + err.Error())
		return
	}
	if cred.CredentialMode != "" {
		credMode = cred.CredentialMode
	}
	if cred.RequiresUserInput || (cred.Password == "" && cred.PrivateKey == "") {
		fail("SFTP 需要堡垒机托管的账号口令，不能在文件通道里手工输入。")
		return
	}
	target, err := s.dialSSH(cred)
	cred.Password = ""
	cred.PrivateKey = ""
	if err != nil {
		fail("连接目标资产失败：" + err.Error())
		return
	}
	defer target.Close()
	ts, err := target.NewSession()
	if err != nil {
		fail("创建远端 SFTP 会话失败：" + err.Error())
		return
	}
	defer ts.Close()
	stdin, err := ts.StdinPipe()
	if err != nil {
		fail("打开远端 SFTP 输入失败：" + err.Error())
		return
	}
	stdout, err := ts.StdoutPipe()
	if err != nil {
		fail("打开远端 SFTP 输出失败：" + err.Error())
		return
	}
	if err := ts.RequestSubsystem("sftp"); err != nil {
		fail("目标资产未接受 SFTP 子系统：" + err.Error())
		return
	}
	if err := req.Reply(true, nil); err != nil {
		return
	}
	replied = true
	outcome = endRemoteClosed
	live.addCloser(ts)
	live.addCloser(target)

	pol := s.filePolicy(true)
	newHash := sha256.New
	if s.prov != nil {
		newHash = func() hash.Hash { return s.prov.SM3New() }
	}
	upstream := &sftpStream{r: stdout, w: stdin, c: ts}
	err = proxySFTP(ctx, channelTouch{Channel: ch, touch: live.touch}, upstream, pol, func(ev sftpEvent) {
		fileCount++
		s.reportFile(sess.SessionID, ev)
	}, newHash)
	if err != nil && ctx.Err() == nil {
		slog.Debug("SFTP 会话结束", "session_no", sess.SessionNo, "err", err)
	}
}

func (s *Server) reportFile(sessionID int64, ev sftpEvent) {
	if s == nil || s.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.client.AddFile(ctx, sessionID, &managerclient.SessionFilePayload{
		Direction: ev.Direction, FileName: ev.Name, FilePath: ev.Path,
		FileSize: ev.Size, FileDigest: ev.Digest, FileType: "sftp",
		PolicyAction: ev.Action, TransferResult: ev.Result,
	})
	if err != nil {
		slog.Warn("文件传输留痕失败", "session_id", sessionID, "path", ev.Path, "err", err)
	}
}

type channelTouch struct {
	ssh.Channel
	touch func()
}

func (c channelTouch) Read(p []byte) (int, error) {
	n, err := c.Channel.Read(p)
	if n > 0 && c.touch != nil {
		c.touch()
	}
	return n, err
}

type sftpStream struct {
	r io.Reader
	w io.Writer
	c io.Closer
}

func (s *sftpStream) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *sftpStream) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *sftpStream) Close() error {
	if s.c != nil {
		return s.c.Close()
	}
	return nil
}

func subsystemName(payload []byte) string {
	name, _, err := readSSHString(payload)
	if err != nil {
		return ""
	}
	return name
}

func splitAccountHint(hint string) (asset, account string) {
	if i := strings.LastIndex(hint, "#"); i > 0 && i < len(hint)-1 {
		return hint[:i], hint[i+1:]
	}
	return hint, ""
}

func isFileProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "ssh", "sftp":
		return true
	default:
		return false
	}
}

func pickSFTPTarget(assets []*managerclient.AssetBrief, hint string) (*managerclient.AssetBrief, *managerclient.AccountBrief, error) {
	assetHint, accountHint := splitAccountHint(hint)
	files := make([]*managerclient.AssetBrief, 0, len(assets))
	for _, asset := range assets {
		if asset != nil && isFileProtocol(asset.Protocol) {
			files = append(files, asset)
		}
	}
	var asset *managerclient.AssetBrief
	switch {
	case assetHint != "":
		asset = matchAsset(files, assetHint)
		if asset == nil {
			return nil, nil, fmt.Errorf("找不到可传输文件的资产 %s。请使用 用户名@资产#账号 登录", assetHint)
		}
	case len(files) == 1:
		asset = files[0]
	default:
		return nil, nil, fmt.Errorf("请使用 用户名@资产#账号 登录后再打开 SFTP")
	}
	acc := pickNamedAccount(asset, accountHint)
	if acc == nil {
		if accountHint != "" {
			return nil, nil, fmt.Errorf("资产 %s 上没有账号 %s", asset.AssetName, accountHint)
		}
		return nil, nil, fmt.Errorf("资产 %s 有多个账号，请使用 用户名@%s#账号 登录", asset.AssetName, asset.AssetName)
	}
	return asset, acc, nil
}

func pickNamedAccount(asset *managerclient.AssetBrief, name string) *managerclient.AccountBrief {
	if asset == nil || len(asset.Accounts) == 0 {
		return nil
	}
	if name != "" {
		for i := range asset.Accounts {
			acc := &asset.Accounts[i]
			if acc.AccountName == name || strconv.FormatInt(acc.AccountID, 10) == name {
				return acc
			}
		}
		return nil
	}
	if len(asset.Accounts) == 1 {
		return &asset.Accounts[0]
	}
	return nil
}
