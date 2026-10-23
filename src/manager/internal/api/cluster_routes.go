package api

import (
	"fmt"
	"net/http"
	"strconv"

	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

// registerClusterGroupRoutes 注册"集群定义与成员"接口。
//
// 与 registerClusterRoutes（节点自注册，/api/v1/cluster/overview|nodes）分开：
// 那条线回答"谁在跑"，这条线回答"运维定义了哪些设备属于哪个集群"。
// 路径用复数 /clusters，避免与服务注册节点的单数前缀混淆。
//
// 读接口要求登录，写接口要求 system:config:write ——
// 改集群成员与维护状态会决定"哪些设备在接运维会话"。
func registerClusterGroupRoutes(mux *http.ServeMux, s *Server) {
	read := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requireAuth(h))
	}
	write := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("system:config:write", h))
	}

	read("GET /api/v1/clusters", s.handleListClusters)
	read("GET /api/v1/clusters/{id}", s.handleGetCluster)

	write("POST /api/v1/clusters", s.handleCreateCluster)
	write("PUT /api/v1/clusters/{id}", s.handleUpdateCluster)
	write("DELETE /api/v1/clusters/{id}", s.handleDeleteCluster)
	write("POST /api/v1/clusters/{id}/members", s.handleAddClusterMembers)
	write("DELETE /api/v1/clusters/{id}/members/{memberId}", s.handleRemoveClusterMember)
	write("PUT /api/v1/clusters/{id}/members/{memberId}", s.handleUpdateClusterMember)
	write("POST /api/v1/clusters/{id}/members/{memberId}/state", s.handleSetClusterMemberState)
	write("POST /api/v1/clusters/{id}/probe", s.handleProbeCluster)
}

func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	items, err := s.cluster.List(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	detail, err := s.cluster.Get(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, detail)
}

// handleCreateCluster 新建集群：集群名 + 设备 IP 列表。
func (s *Server) handleCreateCluster(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		VipAddr     string   `json:"vip_addr"`
		Devices     []string `json:"devices"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	detail, err := s.cluster.Create(r.Context(), service.CreateClusterInput{
		Name:        req.Name,
		Description: req.Description,
		VipAddr:     req.VipAddr,
		Devices:     req.Devices,
	}, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, detail)
}

func (s *Server) handleUpdateCluster(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		VipAddr     string `json:"vip_addr"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.cluster.Update(r.Context(), id, req.Name, req.Description, req.VipAddr); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"cluster_id": id, "updated": true})
}

func (s *Server) handleDeleteCluster(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.cluster.Delete(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"cluster_id": id, "deleted": true})
}

func (s *Server) handleAddClusterMembers(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Devices []string `json:"devices"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	detail, err := s.cluster.AddMembers(r.Context(), id, req.Devices)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, detail)
}

func (s *Server) handleRemoveClusterMember(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	clusterID, memberID, err := clusterMemberPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.cluster.RemoveMember(r.Context(), clusterID, memberID); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"member_id": memberID, "removed": true})
}

func (s *Server) handleUpdateClusterMember(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	clusterID, memberID, err := clusterMemberPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		DeviceName  *string `json:"device_name"`
		Weight      *int    `json:"weight"`
		MaxSessions *int    `json:"max_sessions"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.cluster.UpdateMember(r.Context(), clusterID, memberID,
		req.DeviceName, req.Weight, req.MaxSessions); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"member_id": memberID, "updated": true})
}

func (s *Server) handleSetClusterMemberState(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	clusterID, memberID, err := clusterMemberPath(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		State string `json:"state"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.cluster.SetMemberState(r.Context(), clusterID, memberID, req.State); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"member_id": memberID, "state": req.State})
}

// handleProbeCluster 立即探测集群成员（不等下一个周期）。
func (s *Server) handleProbeCluster(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		httpx.Fail(w, fmt.Errorf("集群服务未启用"))
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	detail, err := s.cluster.ProbeNow(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, detail)
}

// clusterMemberPath 解析集群与成员的路径参数。
func clusterMemberPath(r *http.Request) (int64, int64, error) {
	clusterID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || clusterID <= 0 {
		return 0, 0, fmt.Errorf("集群标识不合法")
	}
	memberID, err := strconv.ParseInt(r.PathValue("memberId"), 10, 64)
	if err != nil || memberID <= 0 {
		return 0, 0, fmt.Errorf("成员标识不合法")
	}
	return clusterID, memberID, nil
}
