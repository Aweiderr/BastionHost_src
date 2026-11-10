package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"bastion.local/server/internal/managerclient"
)

// sessionPlacer 是管理面的会话分配接口。*managerclient.Client 满足它。
type sessionPlacer interface {
	Schedule(ctx context.Context, in managerclient.ScheduleRequest) (*managerclient.Placement, error)
}

// decidePlacement 决定这次新会话留在本节点，还是让客户端改连。
//
// 管理面暂时不可达、或还没有调度接口时，沿用当前节点：否则管理服务抖动等于全集群停服。
// 明确没有健康节点，或当前节点已被摘除，则拒绝，不在本机创建会话。
func decidePlacement(p *managerclient.Placement, err error) (proceed bool, redirect *managerclient.Placement, fail error) {
	if err != nil {
		var ae *managerclient.APIError
		if errors.As(err, &ae) && (ae.Code == "API_4090" || ae.Status == http.StatusConflict) {
			return false, nil, fmt.Errorf("%s", ae.Message)
		}
		slog.Warn("会话调度不可用，沿用当前节点", "err", err)
		return true, nil, nil
	}
	if p == nil || p.Direct || p.AcceptHere || p.SameNode {
		return true, nil, nil
	}
	if p.NotLeader {
		msg := p.Message
		if msg == "" {
			msg = "管理服务正在选主，请稍后重试"
		}
		return false, nil, fmt.Errorf("%s", msg)
	}
	if p.Redirect && p.NodeID != "" {
		return false, p, nil
	}
	return true, nil, nil
}

func (s *Server) askPlacement(ctx context.Context, userID, assetID int64, protocol, clientType string) (proceed bool, redirect *managerclient.Placement, fail error) {
	if s == nil || s.client == nil {
		return true, nil, nil
	}
	p, err := s.client.Schedule(ctx, managerclient.ScheduleRequest{
		UserID: userID, AssetID: assetID, Protocol: protocol, ClientType: clientType,
	})
	return decidePlacement(p, err)
}

func (s *Server) gateNewSession(ctx context.Context, userID, assetID int64, protocol, clientType string) (redirect map[string]any, deny string) {
	proceed, p, err := s.askPlacement(ctx, userID, assetID, protocol, clientType)
	if err != nil {
		return nil, err.Error()
	}
	if proceed || p == nil {
		return nil, ""
	}
	return map[string]any{
		"type": "redirect", "message": redirectText(p),
		"node_id": p.NodeID, "address": p.Address, "host": p.Host,
		"ssh_addr": p.SSHAddr, "webssh_url": p.WebSSHURL, "webdesktop_url": p.WebDesktopURL,
	}, ""
}

func redirectTextFromMap(m map[string]any) string {
	if m == nil {
		return "请改连其它代理节点"
	}
	if msg, _ := m["message"].(string); msg != "" {
		return msg
	}
	return "请改连其它代理节点"
}

func redirectText(p *managerclient.Placement) string {
	if p == nil {
		return "请改连其它代理节点"
	}
	target := p.SSHAddr
	if target == "" {
		target = p.Address
	}
	if target == "" {
		target = p.NodeID
	}
	return fmt.Sprintf("当前节点不接收新会话，请改连 %s", target)
}
