package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

// registerUKeyRoutes 注册 UKey（智能密码钥匙）证书认证相关路由。
//
// 分两组：
//
//   - `/api/v1/auth/ukey/*`：登录链路的一部分，**无需认证**。
//     但"无需认证"不等于"无前置条件"：挑战必须来自已通过账密的账号
//     （由 /auth/login 下发），否则接口会变成任意用户名的探测入口。
//
//   - `/api/v1/ukey/*`：证书与 CA 管理，走权限校验。**这里的权限点按"操作性质"
//     分两类，而不是像早先那样整组共用一个**：
//
//     ① 面向"某个用户的认证方式" —— 绑定/查看他的证书、用内部 CA 为他签发、
//     吊销他的证书。这属于**用户管理**，用 `system:user:update`。
//     把它交给安全职责会让系统管理员在人员管理里彻底失去绑定入口
//     （sys_admin 没有 key 模块权限），而"给某个用户换一把 Key"本来就该由
//     管用户的人来做。
//
//     ② 面向"信任锚与签发链" —— 初始化内部 CA、导入外部 CA 根、查看 CA 台账。
//     这决定**谁签发的证书会被信任**，属于**安全职责**，用 `key:cert:manage`
//     （0008_seed 里中文名「证书链管理」，0009 已授予 sec_admin）。
//
//     两者不能互换：让系统管理员能初始化 CA 等于让他能自建信任根；
//     让安全管理员能改用户绑定则越过了用户管理边界。
//     本次拆分**不需要新增权限点、也不改 0009/0028/0029 的授权**：
//     sys_admin 靠已有的 system:user:update 就能绑定，sec_admin 靠已有的
//     key:cert:manage 管 CA，两个角色各得其所。
func registerUKeyRoutes(mux *http.ServeMux, s *Server) {
	// 登录链路：只保留"凭 challenge_id 取回挑战原文"这一种形式。
	// 曾经存在的"用户名+口令换挑战"（GET 与 POST 两种）已移除，理由见 handleUKeyChallenge。
	mux.HandleFunc("GET /api/v1/auth/ukey/challenge", s.handleUKeyChallenge)
	mux.HandleFunc("POST /api/v1/auth/ukey/login", s.handleUKeyLogin)

	// ① 用户维度：绑定 / 为用户签发 / 吊销（吊销 CA 另需 key:cert:manage，见 handler）
	userManage := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("system:user:update", h))
	}
	// ② 信任锚与签发链维度
	caManage := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("key:cert:manage", h))
	}

	// 列表两侧页面都用：用户管理页看某人的证书，证书链页看 CA 台账 → 任一权限即可
	mux.HandleFunc("GET /api/v1/ukey/certs/expiring",
		s.requirePermission("key:cert:manage", s.handleUKeyExpiringCerts))
	mux.HandleFunc("GET /api/v1/ukey/certs",
		s.requireAnyPermission([]string{"system:user:update", "key:cert:manage"}, s.handleListUKeyCerts))

	userManage("POST /api/v1/ukey/certs", s.handleRegisterUKeyCert)
	userManage("POST /api/v1/ukey/certs/issue", s.handleIssueUKeyCert)
	// 公钥 + 持有证明签发（UKey 中间件没有 CSR 接口，只能用这条路径换发内部证书）
	userManage("POST /api/v1/ukey/certs/issue-by-pubkey/challenge", s.handleUKeyIssuePubkeyChallenge)
	userManage("POST /api/v1/ukey/certs/issue-by-pubkey", s.handleUKeyIssueByPubkey)
	// 吊销：路由放行"两类权限之一"，但**真正需要哪一种取决于证书类型** ——
	// 吊销用户证书是用户管理，吊销 CA 是安全职责。判定在 handler 内做，
	// 因为路由层在拿到证书之前无法知道它是哪一类。
	mux.HandleFunc("POST /api/v1/ukey/certs/{id}/revoke",
		s.requireAnyPermission([]string{"system:user:update", "key:cert:manage"}, s.handleRevokeUKeyCert))

	caManage("GET /api/v1/ukey/ca", s.handleUKeyCA)
	caManage("POST /api/v1/ukey/ca/init", s.handleUKeyCAInit)
	caManage("POST /api/v1/ukey/external-ca", s.handleImportExternalCA)
}

// handleUKeyChallenge 按 challenge_id 取回一份仍可用的 UKey 挑战原文。
//
// **本接口不校验任何口令**，只认 challenge_id（不可预测的一次性标识，
// 由 /auth/login 在下发挑战时返回给已通过账密+图形验证码的调用方）。
// 它的存在意义只有一个：挑战有效期只有 120 秒，客户端在拿到登录响应后
// 可能先弹窗等用户插 Key、输 PIN，随后需要用 challenge_id 重新取回原文。
//
// 历史上这里还有一条"用户名+口令换挑战"的分支（GET 与 POST 两种形式），现已**移除**：
//
//   - 它把口令放进查询串：浏览器历史、反向代理访问日志、Referer 头都能看到；
//   - 它是绕过图形验证码的一条**更便宜的口令校验路径**（/auth/login 每次都要验证码），
//     于是"规则不一致"与"多一个爆破面"同时存在；
//   - 而且它完全冗余：/auth/login 的响应里已经直接带回
//     challenge_id / sign_data / src_data，任何客户端都不需要再换一次。
//
// 把门拆掉，而不是给它补验证码：口令校验面直接消失，问题从根上没有了。
//
// 错误码约定（各情形必须能区分，不能压成 500，也不能互相掩盖）：
//
//	带 username/password（已废弃形式）  → API_4000（400）+ 指明改用 /auth/login
//	缺少 challenge_id                  → API_4000（400）
//	挑战不存在 / 已过期 / 已消费        → API_4000（400），消息分别为三种
//	服务端未装配 UKey 模块              → SYS_5003（501）
func (s *Server) handleUKeyChallenge(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.Fail(w, errcode.New(errcode.ErrNotImplemented, "UKey 证书认证未启用：服务端未装配 UKey 模块，请联系管理员"))
		return
	}
	q := r.URL.Query()
	id := strings.TrimSpace(q.Get("challenge_id"))
	if id == "" {
		// 没带 challenge_id 时，若带了账密就明确告知改用 /auth/login。
		//
		// 为什么不"顺手校验一下口令"：那正是被移除的那条更便宜的爆破路径
		// （不校验图形验证码），而且 /auth/login 的响应里已经带回挑战。
		// 直接忽略这些参数也不行——会让误用者以为口令被校验过，
		// 在一处安全决策上制造错觉比直接报错危险得多。
		if strings.TrimSpace(q.Get("username")) != "" || strings.TrimSpace(q.Get("password")) != "" {
			httpx.Fail(w, errcode.New(errcode.ErrBadRequest,
				"本接口不接受账号口令：请改用 POST /api/v1/auth/login 完成账密与图形验证码校验，"+
					"其响应已直接返回 challenge_id / sign_data / src_data"))
			return
		}
		httpx.Fail(w, errcode.New(errcode.ErrBadRequest,
			"缺少 challenge_id：请先在 POST /api/v1/auth/login 完成登录，响应中已返回 challenge_id"))
		return
	}
	// 带 challenge_id 时忽略其它参数（老客户端可能把用户名一并带上），
	// 但仍然**不做任何口令校验**——本接口只认这个一次性标识。
	//
	// 取回原文时不校验来源 IP：这份原文在下发挑战时就已经交给过客户端，
	// 且 challenge_id 本身不可预测。真正的 IP 绑定在 /auth/ukey/login 校验。
	ch, err := s.ukey.Challenge(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, ch)
}

// 说明：这里曾经有 issueUKeyChallengeWithPassword —— 一条"校验账密后下发挑战"的
// 便捷路径（复用 AuthService.Authenticate）。它已随账密分支一起移除：
// 该路径既不校验图形验证码（比 /auth/login 更便宜），又把口令放进查询串，
// 而 /auth/login 的响应本来就已经带回挑战，属于纯粹的冗余攻击面。
// 删除后，本文件里的 handler **不再调用任何口令校验**（Authenticate）。

// handleUKeyLogin 校验 UKey 断言并签发令牌。
func (s *Server) handleUKeyLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || s.ukey == nil {
		httpx.Fail(w, errcode.New(errcode.ErrNotImplemented, "UKey 证书认证未启用：服务端未装配 UKey 模块，请联系管理员"))
		return
	}
	var req service.UKeyLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if strings.TrimSpace(req.ChallengeID) == "" || strings.TrimSpace(req.Cert) == "" ||
		strings.TrimSpace(req.Signature) == "" {
		httpx.FailCode(w, "API_4000")
		return
	}
	res, authMethod, err := s.auth.VerifyUKeyLogin(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"login": res, "auth_method": authMethod})
}

// handleListUKeyCerts 分页查询用户 UKey 证书。
func (s *Server) handleListUKeyCerts(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	q := r.URL.Query()
	f := repo.CertFilter{
		CertType:   strings.TrimSpace(q.Get("cert_type")),
		Status:     strings.TrimSpace(q.Get("status")),
		CertSource: strings.TrimSpace(q.Get("cert_source")),
		OwnerID:    ukeyQueryInt(q.Get("owner_id")),
		Keyword:    strings.TrimSpace(q.Get("keyword")),
		Page:       int(ukeyQueryInt(q.Get("page"))),
		PageSize:   int(ukeyQueryInt(q.Get("page_size"))),
	}
	items, total, err := s.ukey.ListCerts(r.Context(), f)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	httpx.OK(w, map[string]any{
		"items": items, "total": total, "page": page, "page_size": size,
	})
}

// handleRegisterUKeyCert 登记并绑定一张用户 UKey 证书。
func (s *Server) handleRegisterUKeyCert(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.RegisterCertRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	rec, err := s.ukey.RegisterCert(r.Context(), req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, rec)
}

// handleIssueUKeyCert 用内部签发 CA 为用户签发一张证书（P10 / CSR 流程）。
func (s *Server) handleIssueUKeyCert(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.IssueUserCertRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	rec, err := s.ukey.IssueUserCert(r.Context(), req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, rec)
}

// handleUKeyIssuePubkeyChallenge 为"提交公钥签发证书"下发持有证明挑战。
//
// 挑战是这条链路的安全基础：没有它，"提交公钥即签发"等于允许任何人
// 抄一个公钥来换证书。客户端须用 UKey 内私钥对返回的 src_data 签名，
// 连同公钥一起提交到下面的签发接口。
func (s *Server) handleUKeyIssuePubkeyChallenge(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	ch, err := s.ukey.IssueCertIssueChallenge(r.Context(), req.UserID, clientIP(r), r.UserAgent())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, ch)
}

// handleUKeyIssueByPubkey 校验持有证明后签发并登记证书，返回的 cert_pem 供灌入 UKey。
func (s *Server) handleUKeyIssueByPubkey(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.IssueByPublicKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	rec, err := s.ukey.IssueUserCertByPublicKey(r.Context(), req, clientIP(r), operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, rec)
}

// handleRevokeUKeyCert 吊销证书。
//
// 权限按**证书类型**分派，而不是整条路由共用一个权限点：
//
//	用户证书（cert_type=client）→ system:user:update
//	    吊销某个用户的证书属于"修改该用户的认证方式"，由管用户的人负责。
//
//	CA（cert_type=ca）→ key:cert:manage
//	    一张 CA 被吊销，它签发的**全部**用户证书立刻失效 —— 一次点击就能
//	    让一批人登不进来。这属于信任锚与签发链的安全职责。
//
// 路由层用 requireAnyPermission 先放行"两类之一"，具体要哪一种在这里判：
// 路由层拿不到证书类型，无法提前决定。
//
// 这条判定**必须在服务端做**：前端目前只在用户证书弹窗里提供吊销入口，
// 但"绕过前端直接调接口"正是本系统反复强调的威胁模型 ——
// 依赖前端不放按钮等于没有限制。
func (s *Server) handleRevokeUKeyCert(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.FailCode(w, "API_4000")
		return
	}
	// 先查类型再判权限：没有这一步就无从知道"这次吊销的是不是 CA"。
	// 查不到按 404 处理（不在权限失败日志里泄露"这个 ID 是否存在过"）。
	target, err := s.ukey.GetCert(r.Context(), id)
	if err != nil {
		httpx.FailCode(w, "API_4040")
		return
	}
	needPerm := "system:user:update"
	if target.CertType == repo.CertTypeCA {
		needPerm = "key:cert:manage"
	}
	if !s.hasPermission(r, needPerm) {
		// 走同一条权限失败日志，保持审计一致（permission 记真正需要的那一个）
		s.logPermissionDenied(r, needPerm)
		httpx.FailCode(w, "API_4030")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &req) // 允许空体
	rec, err := s.ukey.RevokeCert(r.Context(), id, req.Reason, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, rec)
}

// handleUKeyCA 返回内部 CA 现状（未初始化时 initialized=false，不报错）。
//
// 不把"未初始化"做成错误：初始化是部署流程里的一步，
// 前端需要先查状态再决定是否展示"初始化"按钮，报错会让页面只能显示红字。
func (s *Server) handleUKeyCA(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	info, ok, err := s.ukey.CAInfo(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	cas, err := s.ukey.ListCACerts(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"initialized": ok, "internal": info, "cas": cas,
		"enabled": s.ukey.Enabled(r.Context()),
	})
}

// handleUKeyCAInit 初始化内部两级 CA（幂等）。
func (s *Server) handleUKeyCAInit(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	info, err := s.ukey.InitCA(r.Context(), operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, info)
}

// handleImportExternalCA 导入外部 CA 根证书为信任锚。
func (s *Server) handleImportExternalCA(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req struct {
		PEM string `json:"pem"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	rec, err := s.ukey.ImportExternalCA(r.Context(), req.PEM, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, rec)
}

// ukeyQueryInt 解析查询参数中的整数，非法或缺失返回 0。
func ukeyQueryInt(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// handleUKeyExpiringCerts 列出临期/已过期的客户端 UKey 证书。
func (s *Server) handleUKeyExpiringCerts(w http.ResponseWriter, r *http.Request) {
	if s.ukey == nil {
		httpx.Fail(w, fmt.Errorf("UKey 证书服务未启用"))
		return
	}
	days := queryInt(r, "within_days", 30)
	list, err := s.ukey.ListExpiringCerts(r.Context(), days)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": list, "within_days": days})
}
