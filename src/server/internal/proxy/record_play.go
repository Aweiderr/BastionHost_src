package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bastion.local/server/internal/record"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

// RecordPlayHandler 返回录像回放接口。
//
// 录像文件与解密密钥都在代理节点本地（管理面拿不到），因此回放由服务端：
//
//	1. 用访问令牌回源管理服务换取身份；
//	2. 校验 `audit:record:play` 权限；
//	3. 解密并**校验摘要链**后返回明文。
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

		path, err := s.findRecord(sessionNo)
		if err != nil {
			httpx.FailCode(w, "API_4040")
			return
		}
		plain, info, err := record.Read(r.Context(), s.prov, s.recordKeyID, path)
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
				slog.Error("录像读取失败", "session_no", sessionNo, "path", path, "err", err)
				httpx.Fail(w, fmt.Errorf("录像读取失败：%v", err))
			}
			return
		}
		slog.Info("录像回放", "session_no", sessionNo, "operator", ident.Username,
			"chunks", info.Chunks, "plain_size", info.PlainSize, "digest", info.Digest)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Record-Digest", info.Digest)
		w.Header().Set("X-Record-Chunks", fmt.Sprint(info.Chunks))
		_, _ = w.Write(plain)
	}
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
