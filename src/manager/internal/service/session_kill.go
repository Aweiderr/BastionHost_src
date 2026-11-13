package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/internalauth"
)

// KillResult 是强制中断的结果。
type KillResult struct {
	SessionID   int64  `json:"session_id"`
	SessionNo   string `json:"session_no"`
	ProxyNode   string `json:"proxy_node"`
	ProxyKilled bool   `json:"proxy_killed"`
	Status      string `json:"status"`
	Message     string `json:"message"`
}

// killSessions 是强制中断要读写的会话记录。*SessionService 满足它。
type killSessions interface {
	Get(ctx context.Context, sessionID int64) (*model.Session, error)
	Terminate(ctx context.Context, sessionID int64, endReason, interruptReason string) (bool, error)
}

// killNodes 按节点 ID 查询代理。不存在时必须返回 (nil, nil)，调用方不能把空指针当节点用。
type killNodes interface {
	Get(ctx context.Context, nodeID string) (*model.Node, error)
}

// SessionKiller 依赖节点查询与内部密钥，用于向代理节点下发掐断指令。
type SessionKiller struct {
	Sessions killSessions
	Nodes    killNodes
	Secret   string
	// Console 是产品内 SQL 会话。proxy_node=console 不是代理，掐断时由它断开数据库连接。
	Console *OracleConsole
	// HTTPClient 可注入以便测试；为空时用默认客户端
	HTTPClient *http.Client
}

// Kill 强制中断进行中的运维会话。
//
// 流程：校验会话仍为 active → 先把库标成 terminated 并写下中断理由 →
// 再 HMAC 调用代理 `/internal/v1/sessions/{id}/kill`。
//
// 必须先落库。代理被掐断后会走 finish，上报 status=closed、
// end_reason=client_disconnect。若这一步发生在 Terminate 之前，
// 条件更新匹配不到 active 行，强制中断会被记成正常断开。
// 库先改掉之后，finish 只会补命令计数和录像，不会覆盖被中断的结论。
// 代理不在线时连接已经不在，审计页也不会长期显示"进行中"。
func (k *SessionKiller) Kill(ctx context.Context, sessionID int64, operator OperatorContext, reason string) (*KillResult, error) {
	if k == nil || k.Sessions == nil {
		return nil, fmt.Errorf("会话服务未启用")
	}
	sess, err := k.Sessions.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.Status != model.SessionStatusActive {
		return &KillResult{
			SessionID: sess.SessionID, SessionNo: sess.SessionNo, ProxyNode: sess.ProxyNode,
			Status: sess.Status, Message: "会话已不在进行中",
		}, nil
	}
	endReason, interruptReason := model.SplitKillReason(reason)

	updated, err := k.Sessions.Terminate(ctx, sessionID, endReason, interruptReason)
	if err != nil {
		return nil, err
	}
	if !updated {
		return k.alreadyEnded(ctx, sess), nil
	}

	// 先落库再拆产品内连接。归档若随后上报 client_disconnect，
	// MergeFinish 会保住 terminated / admin_kill，不会改回正常断开。
	released := false
	if k.Console != nil {
		released = k.Console.Release(sessionID)
	}

	proxyKilled := false
	msg := "已标记终止"
	if released {
		msg = "已断开 SQL 会话并更新库状态"
	}
	if k.shouldNotifyProxy(sess) {
		node, nerr := k.Nodes.Get(ctx, sess.ProxyNode)
		// NodeRepo.Get 在没有这一行时返回 (nil, nil)。产品内 SQL 会话的
		// proxy_node 是逻辑名 console，sys_node 里没有它；已删除的节点也一样。
		// 不能把空指针当成节点去读地址，否则第一次中断会在库已改完之后 panic，
		// 界面看到「系统内部错误」，第二次才发现会话已经不在进行中。
		if nerr != nil || node == nil {
			detail := "节点不存在"
			if nerr != nil {
				detail = nerr.Error()
			}
			msg = "节点信息不可用，仅更新库状态：" + detail
		} else if node.Address == "" {
			msg = "节点未上报地址，仅更新库状态"
		} else {
			proxyReason := interruptReason
			if proxyReason == "" {
				proxyReason = endReason
			}
			if err := k.callProxyKill(ctx, node.Address, sessionID, proxyReason); err != nil {
				msg = "通知代理失败（已更新库状态）：" + err.Error()
			} else {
				proxyKilled = true
				msg = "已通知代理断开并更新库状态"
			}
		}
	}

	if svc, ok := k.Sessions.(*SessionService); ok && svc.oplog != nil {
		l := &model.OperationLog{
			Module: "ops", Action: "session_kill", TargetType: "session",
			TargetID: fmt.Sprint(sess.SessionID), TargetName: sess.SessionNo,
			AfterValue: fmt.Sprintf(`{"end_reason":%q,"interrupt_reason":%q,"proxy_killed":%v,"by":%q}`,
				endReason, interruptReason, proxyKilled, operator.Username),
			Result: "success", ClientIP: operator.ClientIP, OperatorNameSnap: operator.Username,
		}
		if operator.UserID != 0 {
			uid := operator.UserID
			l.OperatorID = &uid
		}
		_ = svc.oplog.Write(ctx, l)
	}

	return &KillResult{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo, ProxyNode: sess.ProxyNode,
		ProxyKilled: proxyKilled, Status: model.SessionStatusTerminated, Message: msg,
	}, nil
}

// shouldNotifyProxy 判断这次中断要不要去叫一台代理。
//
// 产品内 SQL 会话的 proxy_node 是逻辑名 console，不是 sys_node。
// 空节点名同样没有可通知的地址。这两种都只更新库并拆本进程里的连接。
func (k *SessionKiller) shouldNotifyProxy(sess *model.Session) bool {
	if k == nil || sess == nil || k.Nodes == nil || k.Secret == "" {
		return false
	}
	if sess.ProxyNode == "" || sess.ProxyNode == oracleConsoleNode || sess.ClientType == oracleConsoleClient {
		return false
	}
	return true
}

// alreadyEnded 在落库时发现会话已不再是 active 时返回当前状态。
func (k *SessionKiller) alreadyEnded(ctx context.Context, sess *model.Session) *KillResult {
	res := &KillResult{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo, ProxyNode: sess.ProxyNode,
		Status: sess.Status, Message: "会话已不在进行中",
	}
	latest, err := k.Sessions.Get(ctx, sess.SessionID)
	if err == nil && latest != nil {
		res.Status = latest.Status
		res.SessionNo = latest.SessionNo
		res.ProxyNode = latest.ProxyNode
	}
	return res
}

func (k *SessionKiller) callProxyKill(ctx context.Context, address string, sessionID int64, reason string) error {
	client := k.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	base := address
	if len(base) > 0 && base[0] == ':' {
		base = "http://127.0.0.1" + base
	}
	if !(len(base) >= 4 && (base[:4] == "http")) {
		base = "http://" + base
	}
	path := "/internal/v1/sessions/" + strconv.FormatInt(sessionID, 10) + "/kill"
	body, _ := json.Marshal(map[string]string{"reason": reason})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	ts := time.Now().Unix()
	req.Header.Set(internalauth.HeaderNode, "manager")
	req.Header.Set(internalauth.HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(internalauth.HeaderSignature, internalauth.Sign(k.Secret, "manager", ts, http.MethodPost, path))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("proxy HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
