package api

import (
	"fmt"
	"net/http"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/httpx"
)

// registerClusterRoutes 注册集群节点管理路由。
//
// 读接口要求登录：节点清单里有内网地址、版本、活跃会话数与心跳时间，
// 属于运维内部信息，不该对未认证请求开放。
// 写接口要求 system:config:write —— 停用或摘除节点会直接决定
// "谁能建立运维会话"，属于系统管理员的职责，不该让任意登录用户操作。
func registerClusterRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/v1/cluster/overview", s.requireAuth(s.handleClusterOverview))
	mux.HandleFunc("GET /api/v1/cluster/nodes", s.requireAuth(s.handleListNode))

	write := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("system:config:write", h))
	}
	write("POST /api/v1/cluster/nodes/{id}/state", s.handleSetNodeState)
	write("PUT /api/v1/cluster/nodes/{id}", s.handleUpdateNode)
	write("DELETE /api/v1/cluster/nodes/{id}", s.handleDeleteNode)
}

// handleClusterOverview 返回集群概览（部署模式、节点计数、容量与设计目标）。
func (s *Server) handleClusterOverview(w http.ResponseWriter, r *http.Request) {
	if s.node == nil {
		httpx.Fail(w, fmt.Errorf("集群节点服务未启用"))
		return
	}
	ov, err := s.node.Overview(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, ov)
}

// handleListNode 返回节点列表；?role=proxy 可只看代理节点。
func (s *Server) handleListNode(w http.ResponseWriter, r *http.Request) {
	if s.node == nil {
		httpx.Fail(w, fmt.Errorf("集群节点服务未启用"))
		return
	}
	items, err := s.node.List(r.Context(), r.URL.Query().Get("role"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}

// handleSetNodeState 设置节点运维状态（active / draining / disabled）。
func (s *Server) handleSetNodeState(w http.ResponseWriter, r *http.Request) {
	if s.node == nil {
		httpx.Fail(w, fmt.Errorf("集群节点服务未启用"))
		return
	}
	var req struct {
		State string `json:"state"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	view, err := s.node.SetState(r.Context(), r.PathValue("id"), req.State)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, view)
}

// handleUpdateNode 修改节点名称、权重或会话上限。
func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	if s.node == nil {
		httpx.Fail(w, fmt.Errorf("集群节点服务未启用"))
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Weight      *int    `json:"weight"`
		MaxSessions *int    `json:"max_sessions"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	view, err := s.node.Update(r.Context(), r.PathValue("id"), repo.NodeUpdate{
		NodeName:    req.Name,
		Weight:      req.Weight,
		MaxSessions: req.MaxSessions,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, view)
}

// handleDeleteNode 从集群中移除节点记录。
func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	if s.node == nil {
		httpx.Fail(w, fmt.Errorf("集群节点服务未启用"))
		return
	}
	id := r.PathValue("id")
	if err := s.node.Remove(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"node_id": id, "removed": true})
}
