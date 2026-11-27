package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bastion.local/server/internal/managerclient"
	"bastion.local/server/internal/record"
	"bastion.local/shared/auditmask"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

// RecordPlayHandler 返回录像回放接口。
//
// 录像文件与解密密钥都在代理节点本地（管理面拿不到），因此回放由服务端：
//
//  1. 用访问令牌回源管理服务换取身份；
//  2. 校验 `audit:record:play` 权限；
//  3. 解密并**校验摘要链**后返回明文。
//
// 校验失败一律拒绝返回明文 —— 录像不可信时回放内容没有证据价值。
func (s *Server) RecordPlayHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionNo := r.PathValue("sessionNo")
		if sessionNo == "" || strings.ContainsAny(sessionNo, `/\`) {
			httpx.FailCode(w, "API_4000")
			return
		}
		token := bearerOf(r)
		if token == "" {
			httpx.FailCode(w, "API_4010")
			return
		}
		actx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		ident, err := s.client.VerifyToken(actx, token)
		cancel()
		if err != nil {
			httpx.FailCode(w, "API_4010")
			return
		}
		// 审计管理员或超级管理员可回放
		if !hasPermission(ident.Permissions, "audit:record:play") &&
			!hasPermission(ident.Permissions, "audit:session:list") {
			httpx.FailCode(w, "API_4030")
			return
		}
		// 去掉水印的查询不会生效，但必须留下审计。即使后面录像读失败也要记。
		if watermarkDisabled(r) {
			s.noteWatermarkBypass(r.Context(), ident, sessionNo, clientIPOf(r), "回放请求携带 watermark=off")
		}

		path, plain, info, err := s.readRecording(r.Context(), sessionNo)
		if err != nil {
			// "没录完"与"不可信"必须分开告诉调用方：
			// 前者是运维事件（会话被强制停止），后者是安全事件（篡改）。
			// 一律回 500"系统内部错误"，前端只能显示"录像不可回放"，
			// 把真实原因整个吞掉 —— 排查时只能靠猜。
			switch {
			case errors.Is(err, record.ErrNotSealed):
				slog.Warn("录像未正常封存，拒绝回放", "session_no", sessionNo, "path", path, "err", err)
				httpx.Fail(w, errcode.Newf(errcode.ErrRecordNotSealed,
					"该会话的录像未正常封存（会话可能被强制停止），无法回放"))
			case errors.Is(err, record.ErrCorrupted):
				slog.Error("录像完整性校验失败，拒绝回放", "session_no", sessionNo, "path", path, "err", err)
				httpx.Fail(w, errcode.Newf(errcode.ErrRecordCorrupted,
					"录像完整性校验未通过，内容不可信：%v", err))
			default:
				if os.IsNotExist(err) || strings.Contains(err.Error(), "不存在") {
					httpx.FailCode(w, "API_4040")
					return
				}
				slog.Error("录像读取失败", "session_no", sessionNo, "path", path, "err", err)
				httpx.Fail(w, fmt.Errorf("录像读取失败：%v", err))
			}
			return
		}
		slog.Info("录像回放", "session_no", sessionNo, "operator", ident.Username,
			"chunks", info.Chunks, "plain_size", info.PlainSize, "digest", info.Digest)

		kind := "terminal"
		if isGraphicalRecord(plain) {
			kind = "graphical"
		}
		policy := s.playbackPolicy(r.Context(), ident.TenantID)
		reveal := false
		if r.URL.Query().Get("reveal") == "1" {
			reveal = s.askReveal(r.Context(), ident, sessionNo, clientIPOf(r))
		}
		body, redacted := applyPlayback(plain, policy.Rules, reveal)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Record-Kind", kind)
		w.Header().Set("X-Record-Digest", info.Digest)
		w.Header().Set("X-Record-Chunks", fmt.Sprint(info.Chunks))
		// 水印正文可能含中文，不放进响应头。头只声明必须展示，样式用 ASCII。
		w.Header().Set("X-Watermark-Required", "1")
		if policy.Watermark.Style != "" {
			w.Header().Set("X-Watermark-Style", policy.Watermark.Style)
		} else {
			w.Header().Set("X-Watermark-Style", auditmask.StyleDiagonal)
		}
		if redacted {
			w.Header().Set("X-Record-Redacted", "1")
		}
		if reveal {
			w.Header().Set("X-Record-Reveal", "1")
		}
		_, _ = w.Write(body)
	}
}

// playbackAuditor 是回放时向管理面要脱敏规则、并记录绕过的能力。
// 管理面不可用时回放仍按内置规则脱敏，不把原文直接送出。
type playbackAuditor interface {
	DisplayPolicy(ctx context.Context, tenantID int64) (auditmask.Policy, error)
	NoteWatermarkBypass(ctx context.Context, userID int64, username string, tenantID int64, sessionNo, clientIP, reason string) error
	AskReveal(ctx context.Context, userID int64, username string, tenantID int64, sessionNo, clientIP string) (bool, error)
}

type cachedPolicy struct {
	at     time.Time
	policy auditmask.Policy
}

func (s *Server) auditor() playbackAuditor {
	a, _ := s.client.(playbackAuditor)
	return a
}

func (s *Server) playbackPolicy(ctx context.Context, tenantID int64) auditmask.Policy {
	fallback := auditmask.Policy{Rules: auditmask.BuiltinRules(), Watermark: auditmask.DefaultWatermark()}
	s.displayMu.Lock()
	if item, ok := s.displayCache[tenantID]; ok && time.Since(item.at) < 15*time.Second {
		s.displayMu.Unlock()
		return item.policy
	}
	s.displayMu.Unlock()
	auditor := s.auditor()
	if auditor == nil {
		return fallback
	}
	policy, err := auditor.DisplayPolicy(ctx, tenantID)
	if err != nil {
		return fallback
	}
	if _, nerr := policy.Watermark.Normalize(); nerr != nil {
		policy.Watermark = auditmask.DefaultWatermark()
	}
	s.displayMu.Lock()
	if s.displayCache == nil {
		s.displayCache = map[int64]cachedPolicy{}
	}
	s.displayCache[tenantID] = cachedPolicy{at: time.Now(), policy: policy}
	s.displayMu.Unlock()
	return policy
}

func (s *Server) noteWatermarkBypass(ctx context.Context, ident *managerclient.OpsIdentity, sessionNo, ip, reason string) {
	auditor := s.auditor()
	if auditor == nil || ident == nil {
		slog.Warn("回放请求试图关闭水印", "session_no", sessionNo, "reason", reason)
		return
	}
	if err := auditor.NoteWatermarkBypass(ctx, ident.UserID, ident.Username, ident.TenantID, sessionNo, ip, reason); err != nil {
		slog.Warn("水印绕过审计写入失败", "session_no", sessionNo, "err", err)
	}
}

func (s *Server) askReveal(ctx context.Context, ident *managerclient.OpsIdentity, sessionNo, ip string) bool {
	auditor := s.auditor()
	if auditor == nil || ident == nil {
		return false
	}
	ok, err := auditor.AskReveal(ctx, ident.UserID, ident.Username, ident.TenantID, sessionNo, ip)
	if err != nil {
		slog.Warn("原文查看授权失败，按脱敏回放", "session_no", sessionNo, "err", err)
		return false
	}
	return ok
}

func applyPlayback(plain []byte, rules []auditmask.Rule, reveal bool) ([]byte, bool) {
	if reveal {
		return plain, false
	}
	out := auditmask.RedactPlayback(plain, rules)
	return out, !bytes.Equal(out, plain)
}

func watermarkDisabled(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("watermark"))) {
	case "0", "off", "false":
		return true
	default:
		return false
	}
}

func clientIPOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// readRecording 读取并校验录像。共享存储优先：对象不在本机时仍能打开。
func (s *Server) readRecording(ctx context.Context, sessionNo string) (string, []byte, *record.Info, error) {
	if s.recordStore != nil {
		key, raw, err := record.ReadSession(ctx, s.recordStore, sessionNo, time.Now())
		if err != nil {
			return "", nil, nil, err
		}
		var (
			plain []byte
			info  *record.Info
		)
		if s.recordCipher != nil {
			plain, info, err = record.Decode(ctx, s.prov, s.recordCipher, raw)
		} else {
			plain, info, err = record.DecodeKey(ctx, s.prov, s.recordKeyID, raw)
		}
		return key, plain, info, err
	}
	path, err := s.findRecord(sessionNo)
	if err != nil {
		return "", nil, nil, err
	}
	plain, info, err := record.Read(ctx, s.prov, s.recordKeyID, path)
	return path, plain, info, err
}

// findRecord 在录像根目录下按会话号查找文件。
//
// 目录按 yyyy/MM/dd 分层，这里按天倒序探测最近 90 天，
// 避免对大目录做全量遍历。
func (s *Server) findRecord(sessionNo string) (string, error) {
	root := s.cfg.Record.LocalDir
	if root == "" {
		return "", fmt.Errorf("录像目录未配置")
	}
	now := time.Now()
	for i := 0; i < 90; i++ {
		d := now.AddDate(0, 0, -i)
		p := filepath.Join(root, d.Format("2006"), d.Format("01"), d.Format("02"), sessionNo+".bsr")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("录像文件不存在")
}

// bearerOf 从 Authorization 头或 token 查询参数取令牌。
//
// 支持 query 是为了兼容浏览器端直接打开下载链接的场景；
// 正规调用应使用 Authorization 头，避免令牌进入访问日志。
func bearerOf(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}
