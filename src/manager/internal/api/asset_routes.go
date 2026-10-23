package api

import (
	"net/http"
	"strconv"

	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

// registerAssetRoutes 注册资产与账号管理路由（对应需求 FR-02）。
func registerAssetRoutes(mux *http.ServeMux, s *Server) {
	// 资产
	mux.HandleFunc("GET /api/v1/assets", s.requirePermission("asset:asset:list", s.handleListAssets))
	mux.HandleFunc("POST /api/v1/assets", s.requirePermission("asset:asset:create", s.handleCreateAsset))
	mux.HandleFunc("GET /api/v1/assets/{id}", s.requirePermission("asset:asset:list", s.handleGetAsset))
	mux.HandleFunc("PUT /api/v1/assets/{id}", s.requirePermission("asset:asset:update", s.handleUpdateAsset))
	mux.HandleFunc("DELETE /api/v1/assets/{id}", s.requirePermission("asset:asset:delete", s.handleDeleteAsset))

	// 资产分组
	mux.HandleFunc("GET /api/v1/asset-groups/tree", s.requirePermission("asset:asset:list", s.handleGroupTree))
	mux.HandleFunc("POST /api/v1/asset-groups", s.requirePermission("asset:asset:create", s.handleCreateGroup))
	mux.HandleFunc("DELETE /api/v1/asset-groups/{id}", s.requirePermission("asset:asset:delete", s.handleDeleteGroup))

	// 托管账号
	mux.HandleFunc("GET /api/v1/assets/{id}/accounts", s.requirePermission("asset:account:list", s.handleListAccounts))
	mux.HandleFunc("POST /api/v1/assets/{id}/accounts", s.requirePermission("asset:account:manage", s.handleCreateAccount))
	mux.HandleFunc("PUT /api/v1/accounts/{id}", s.requirePermission("asset:account:manage", s.handleUpdateAccount))
	mux.HandleFunc("DELETE /api/v1/accounts/{id}", s.requirePermission("asset:account:manage", s.handleDeleteAccount))
	mux.HandleFunc("POST /api/v1/accounts/{id}/password", s.requirePermission("asset:account:manage", s.handleUpdateAccountPassword))
	// 查看明文口令：高敏操作，需专用权限并强制审计
	mux.HandleFunc("POST /api/v1/accounts/{id}/reveal", s.requirePermission("asset:account:viewpwd", s.handleRevealPassword))

	// 凭据加密密钥信息（便于运维核对）
	mux.HandleFunc("GET /api/v1/assets/credential-key", s.requirePermission("asset:account:list", s.handleCredentialKey))
}

// pathID 解析路径中的 ID 参数。
func pathID(r *http.Request) (int64, error) {
	raw := r.PathValue("id")
	if raw == "" {
		return 0, errcode.New(errcode.ErrBadRequest, "缺少资源 ID")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errcode.Newf(errcode.ErrBadRequest, "资源 ID 非法: %s", raw)
	}
	return id, nil
}

// queryInt 读取查询参数中的整数。
func queryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// ---------- 资产 ----------

func (s *Server) handleListAssets(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	f := repo.AssetFilter{
		TenantID:  claimsFrom(r).TenantID,
		GroupID:   int64(queryInt(r, "group_id", 0)),
		Keyword:   r.URL.Query().Get("keyword"),
		AssetType: r.URL.Query().Get("asset_type"),
		Protocol:  r.URL.Query().Get("protocol"),
		Level:     r.URL.Query().Get("level"),
		Status:    r.URL.Query().Get("status"),
		Page:      queryInt(r, "page", 1),
		PageSize:  queryInt(r, "page_size", 20),
	}
	list, total, err := s.asset.ListAssets(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total,
		"page": f.Page, "page_size": f.PageSize,
	})
}

func (s *Server) handleCreateAsset(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.CreateAssetRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	a, err := s.asset.CreateAsset(r.Context(), req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, a)
}

func (s *Server) handleGetAsset(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	a, err := s.asset.GetAsset(r.Context(), id)
	if err != nil {
		httpx.Fail(w, errcode.New(errcode.ErrNotFound, "资产不存在"))
		return
	}
	httpx.OK(w, a)
}

func (s *Server) handleUpdateAsset(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req service.CreateAssetRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	a, err := s.asset.UpdateAsset(r.Context(), id, req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, a)
}

func (s *Server) handleDeleteAsset(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.asset.DeleteAsset(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"deleted": true, "asset_id": id})
}

// ---------- 资产分组 ----------

func (s *Server) handleGroupTree(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	tree, err := s.asset.GroupTree(r.Context(), claimsFrom(r).TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": tree})
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req struct {
		GroupName string `json:"group_name"`
		ParentID  int64  `json:"parent_id"`
		Remark    string `json:"remark"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	g, err := s.asset.CreateGroup(r.Context(), req.GroupName, req.ParentID, req.Remark, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.asset.DeleteGroup(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"deleted": true, "group_id": id})
}

// ---------- 托管账号 ----------

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	assetID, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	list, err := s.asset.ListAccounts(r.Context(), assetID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": list, "total": len(list)})
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	assetID, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req service.CreateAccountRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	a, err := s.asset.CreateAccount(r.Context(), assetID, req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, a)
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req service.CreateAccountRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	a, err := s.asset.UpdateAccount(r.Context(), id, req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, a)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.asset.DeleteAccount(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"deleted": true, "account_id": id})
}

func (s *Server) handleUpdateAccountPassword(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Password   string `json:"password"`
		ChangeType string `json:"change_type"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	changeType := req.ChangeType
	if changeType == "" {
		changeType = "manual"
	}
	if err := s.asset.UpdateAccountPassword(r.Context(), id, req.Password, changeType, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"updated": true, "account_id": id})
}

// handleRevealPassword 查看账号明文口令（高敏操作，强制审计）。
func (s *Server) handleRevealPassword(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	pwd, err := s.asset.RevealAccountPassword(r.Context(), id, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"account_id": id,
		"password":   pwd,
		"warning":    "本次查看已记入审计日志",
	})
}

func (s *Server) handleCredentialKey(w http.ResponseWriter, r *http.Request) {
	if s.asset == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	keyID, err := s.asset.CredentialKeyID(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"key_id":  keyID,
		"usage":   "credential_encrypt",
		"note":    "账号口令使用该密钥加密（SM4），密钥材料由密码服务托管",
		"provider": providerName(s),
	})
}
