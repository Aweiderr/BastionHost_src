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

// SessionKiller 依赖节点查询与内部密钥，用于向代理节点下发掐断指令。
type SessionKiller struct {
	Sessions *SessionService
	Nodes    *NodeService
	Secret   string
	// HTTPClient 可注入以便测试；为空时用默认客户端
	HTTPClient *http.Client
}

// Kill 强制中断进行中的运维会话。
//
// 流程：校验会话仍为 active → 按 proxy_node 定位代理 → HMAC 调用代理
// `/internal/v1/sessions/{id}/kill` → 无论代理是否在线，都将库中状态置为 terminated。
// 代理不在线时会话会因连接已断而自然结束；库状态先改掉避免审计页长期显示"进行中"。
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
	if reason == "" {
		reason = "admin_kill"
	}

	proxyKilled := false
	msg := "已标记终止"
	if sess.ProxyNode != "" && k.Nodes != nil && k.Secret != "" {
		node, nerr := k.Nodes.Get(ctx, sess.ProxyNode)
		if nerr != nil {
			msg = "节点信息不可用，仅更新库状态：" + nerr.Error()
		} else if node.Address == "" {
			msg = "节点未上报地址，仅更新库状态"
		} else {
			if err := k.callProxyKill(ctx, node.Address, sessionID, reason); err != nil {
				msg = "通知代理失败（已更新库状态）：" + err.Error()
			} else {
				proxyKilled = true
				msg = "已通知代理断开并更新库状态"
			}
		}
	}

	if err := k.Sessions.sessionRepo.Terminate(ctx, sessionID, reason); err != nil {
		return nil, err
	}

	if k.Sessions.oplog != nil {
		l := &model.OperationLog{
			Module: "ops", Action: "session_kill", TargetType: "session",
			TargetID: fmt.Sprint(sess.SessionID), TargetName: sess.SessionNo,
			AfterValue: fmt.Sprintf(`{"reason":%q,"proxy_killed":%v,"by":%q}`, reason, proxyKilled, operator.Username),
			Result:     "success", ClientIP: operator.ClientIP, OperatorNameSnap: operator.Username,
		}
		if operator.UserID != 0 {
			uid := operator.UserID
			l.OperatorID = &uid
		}
		_ = k.Sessions.oplog.Write(ctx, l)
	}

	return &KillResult{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo, ProxyNode: sess.ProxyNode,
		ProxyKilled: proxyKilled, Status: model.SessionStatusTerminated, Message: msg,
	}, nil
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
