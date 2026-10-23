package api

import (
	"net/http"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
	"bastion.local/shared/internalauth"
)

// registerInternalRoutes 注册服务间接口（服务端 → 管理服务）。
//
// 这些接口不对外暴露：以 HMAC-SM3 签名 + 时间戳窗口做服务间认证，
// 并叠加网络层限制（TrustedCIDRs，默认仅 loopback；生产应配置代理节点网段）。
//
// 之所以让服务端回源而不是直连数据库：用户口令散列与凭据密钥只存在于
// 管理面，代理节点因此可以在 DMZ 横向扩展而不扩散密钥。
func registerInternalRoutes(mux *http.ServeMux, s *Server) {
	// 网络层隔离在 HMAC 校验之前：未受信来源连签名都不必验，直接拒绝。
	cidrs := s.cfg.Internal.TrustedCIDRs
	h := func(next http.HandlerFunc) http.HandlerFunc {
		return requireTrustedSource(cidrs, s.requireNode(next))
	}

	mux.HandleFunc("POST /internal/v1/ops/verify", h(s.handleOpsVerify))
	mux.HandleFunc("POST /internal/v1/ops/verify-token", h(s.handleOpsVerifyToken))
	mux.HandleFunc("GET /internal/v1/ops/assets", h(s.handleOpsAssets))
	mux.HandleFunc("GET /internal/v1/ops/grant", h(s.handleOpsGrant))
	mux.HandleFunc("POST /internal/v1/ops/credential", h(s.handleOpsCredential))
	mux.HandleFunc("POST /internal/v1/ops/hostkey-learn", h(s.handleHostKeyLearn))
	mux.HandleFunc("GET /internal/v1/policies/commands", h(s.handleInternalPolicies))
	mux.HandleFunc("POST /internal/v1/sessions", h(s.handleSessionStart))
	mux.HandleFunc("POST /internal/v1/sessions/{id}/commands", h(s.handleSessionCommand))
	mux.HandleFunc("POST /internal/v1/sessions/{id}/finish", h(s.handleSessionFinish))
	// 节点心跳：代理节点定期上报负载，并取回"是否允许接收新会话"
	mux.HandleFunc("POST /internal/v1/nodes/heartbeat", h(s.handleNodeHeartbeat))
}

// handleNodeHeartbeat 处理代理节点心跳。
//
// 节点身份以**签名头**为准，而不是请求体里的 node_id：
// 签名覆盖了 node 头，请求体没有 —— 只信请求体的话，
// 任一持有共享密钥的节点都能冒充别的节点上报负载甚至改变其可用性。
func (s *Server) handleNodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	if s.node == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var in repo.HeartbeatInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if nodeID := r.Header.Get(internalauth.HeaderNode); nodeID != "" {
		in.NodeID = nodeID
	}
	ack, err := s.node.Heartbeat(r.Context(), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, ack)
}

// requireNode 校验服务间签名。
func (s *Server) requireNode(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		secret := s.cfg.Internal.Secret
		if secret == "" {
			// 未配置密钥时直接拒绝，避免"忘记配置即可匿名调用"的降级风险
			httpx.FailCode(w, "API_4010")
			return
		}
		ts, err := internalauth.ParseTimestamp(r.Header.Get(internalauth.HeaderTimestamp))
		if err != nil {
			httpx.FailCode(w, "API_4010")
			return
		}
		// 用 RequestURI（含 query）参与签名：若只签 path，
		// 攻击者可在内网篡改 user_id 之类的参数越权查询资产与授权。
		if err := internalauth.Verify(secret,
			r.Header.Get(internalauth.HeaderNode),
			r.Header.Get(internalauth.HeaderSignature),
			ts, r.Method, r.URL.RequestURI(), s.cfg.Internal.Window); err != nil {
			httpx.FailCode(w, "API_4010")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleOpsVerify(w http.ResponseWriter, r *http.Request) {
	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
		ClientIP string `json:"client_ip"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ip := req.ClientIP
	if ip == "" {
		ip = clientIP(r)
	}
	id, err := s.ops.VerifyCredential(r.Context(), req.Username, req.Password, req.TOTPCode, ip)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, id)
}

func (s *Server) handleOpsVerifyToken(w http.ResponseWriter, r *http.Request) {
	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Token == "" {
		httpx.FailCode(w, "API_4000")
		return
	}
	id, err := s.ops.VerifyToken(r.Context(), req.Token)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, id)
}

func (s *Server) handleOpsAssets(w http.ResponseWriter, r *http.Request) {	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	userID := int64(queryInt(r, "user_id", 0))
	if userID == 0 {
		httpx.FailCode(w, "API_4000")
		return
	}
	items, err := s.ops.AccessibleAssets(r.Context(), userID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleOpsGrant(w http.ResponseWriter, r *http.Request) {
	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	userID := int64(queryInt(r, "user_id", 0))
	assetID := int64(queryInt(r, "asset_id", 0))
	accountID := int64(queryInt(r, "account_id", 0))
	if userID == 0 || assetID == 0 {
		httpx.FailCode(w, "API_4000")
		return
	}
	check, err := s.ops.CheckGrant(r.Context(), userID, assetID, accountID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, check)
}

func (s *Server) handleOpsCredential(w http.ResponseWriter, r *http.Request) {
	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req struct {
		UserID      int64  `json:"user_id"`
		Username    string `json:"username"`
		AssetID     int64  `json:"asset_id"`
		AccountID   int64  `json:"account_id"`
		SessionNo   string `json:"session_no"`
		ClientIP    string `json:"client_ip"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.UserID == 0 || req.AssetID == 0 {
		httpx.FailCode(w, "API_4000")
		return
	}
	ip := req.ClientIP
	if ip == "" {
		ip = clientIP(r)
	}
	cred, err := s.ops.FetchCredential(r.Context(),
		service.OperatorContext{UserID: req.UserID, Username: req.Username, ClientIP: ip},
		req.UserID, req.AssetID, req.AccountID, req.SessionNo)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, cred)
}

func (s *Server) handleInternalPolicies(w http.ResponseWriter, r *http.Request) {
	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	rules, err := s.ops.CommandPolicies(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": rules, "total": len(rules)})
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	if s.session == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.StartSessionRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	sess, err := s.session.Start(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, sess)
}

func (s *Server) handleSessionCommand(w http.ResponseWriter, r *http.Request) {
	if s.session == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var c model.SessionCommand
	if err := decodeJSON(r, &c); err != nil {
		httpx.Fail(w, err)
		return
	}
	saved, err := s.session.AddCommand(r.Context(), id, &c)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, saved)
}

func (s *Server) handleSessionFinish(w http.ResponseWriter, r *http.Request) {
	if s.session == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var in repo.SessionCloseInput
	if err := decodeJSON(r, &in); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.session.Finish(r.Context(), id, in); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"session_id": id, "finished": true})
}

// handleHostKeyLearn 接受代理节点 TOFU 回写的主机指纹。
func (s *Server) handleHostKeyLearn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AssetID     int64  `json:"asset_id"`
		Fingerprint string `json:"fingerprint"`
		Policy      string `json:"policy"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	if err := s.ops.LearnHostKey(r.Context(), req.AssetID, req.Fingerprint, req.Policy); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"learned": true})
}
