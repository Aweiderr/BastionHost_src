package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/auth"
	bastioncrypto "bastion.local/shared/crypto"
	"bastion.local/shared/crypto/soft"
)

// TestUKeyRoutePermissions 在真库上固定 UKey 接口的**权限归属**。
//
// 背景（用户明确的设计决策）：UKey 相关接口曾被整组挂在一个权限点下，
// 副作用是系统管理员在人员管理里失去了"给用户绑定 UKey 证书"的入口。
// 现按**操作性质**拆成两类：
//
//   - 面向"某个用户的认证方式"（绑定 / 为用户签发 / 吊销其证书）→ system:user:update；
//   - 面向"信任锚与签发链"（初始化内部 CA / 导入外部 CA / 查看 CA 台账）→ key:cert:manage；
//   - 证书列表被两类页面使用 → 任一满足即可；
//   - 吊销接口按**证书类型**判权：用户证书要 system:user:update，CA 要 key:cert:manage。
//
// 本测试覆盖四个角色 × 全部接口。**只在临时库上运行**。
//
// 用独立的 `BASTION_UKEY_PERM_TEST_DSN` 而不是与 service 包共用
// `BASTION_UKEY_TEST_DSN`：`go test ./...` 会**并行**跑不同包，
// 两个包共用同一个库时会互相破坏（本测试会创建 CA、吊销 CA、写入用户证书，
// 而 service 包的流程测试要求库处于干净状态）。分开两个库是唯一稳妥的做法。
func TestUKeyRoutePermissions(t *testing.T) {
	dsn := os.Getenv("BASTION_UKEY_PERM_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 BASTION_UKEY_PERM_TEST_DSN，跳过 UKey 权限集成测试")
	}
	if !strings.Contains(dsn, "_itest") {
		t.Fatalf("拒绝执行：临时库名必须包含 _itest（防止误连业务库）")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接临时库失败: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("临时库不可用: %v", err)
	}

	prov, err := soft.New(soft.Options{
		Backend:     string(bastioncrypto.BackendGo),
		KeyStoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("构造密码服务失败: %v", err)
	}
	userRepo := repo.NewUserRepo(pool)
	roleRepo := repo.NewRoleRepo(pool)
	configRepo := repo.NewConfigRepo(pool)
	certRepo := repo.NewCertRepo(pool)
	challengeRepo := repo.NewChallengeRepo(pool)
	ukeySvc := service.NewUKeyService(certRepo, challengeRepo, userRepo, configRepo, prov, nil)

	tm, err := auth.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("构造令牌管理器失败: %v", err)
	}
	authSvc := service.NewAuthService(userRepo, roleRepo, configRepo, tm, &config.Config{}, nil)
	srv := &Server{auth: authSvc, ukey: ukeySvc, roleRepo: roleRepo}
	mux := http.NewServeMux()
	registerUKeyRoutes(mux, srv)

	// ---- 测试用户与角色（用户名带时间戳，避免与上一轮残留冲突）----
	suffix := time.Now().Format("150405")
	mkUser := func(role string) *model.User {
		u := &model.User{
			TenantID: 0, Username: "perm-" + role + "-" + suffix, DisplayName: "权限测试 " + role,
			PasswordHash: "x", PasswordSalt: "y", PasswordAlgo: "SM3",
			PasswordExpireDays: 90, Status: "active", UserType: "local",
		}
		if err := userRepo.Create(ctx, u); err != nil {
			t.Fatalf("创建用户 %s 失败: %v", role, err)
		}
		if err := userRepo.AssignRoles(ctx, u.UserID, []string{role}); err != nil {
			t.Fatalf("分配角色 %s 失败: %v", role, err)
		}
		return u
	}
	users := map[string]*model.User{
		"sys_admin":   mkUser("sys_admin"),
		"sec_admin":   mkUser("sec_admin"),
		"super_admin": mkUser("super_admin"),
		"operator":    mkUser("operator"),
	}
	tokens := map[string]string{}
	for role, u := range users {
		tok, terr := tm.Issue(auth.Claims{
			UserID: u.UserID, Username: u.Username, Roles: []string{role}, Scope: auth.ScopeAccess,
		}, 5*time.Minute)
		if terr != nil {
			t.Fatalf("签发 %s 令牌失败: %v", role, terr)
		}
		tokens[role] = tok
	}
	call := func(method, target, role string) int {
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set("Authorization", "Bearer "+tokens[role])
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	// ---- 准备被测对象：内部 CA（用 sec_admin 走接口初始化）+ 一张用户证书 ----
	if code := call(http.MethodPost, "/api/v1/ukey/ca/init", "sec_admin"); code != http.StatusOK {
		t.Fatalf("sec_admin 初始化内部 CA 应返回 200，实际 %d", code)
	}
	roots, err := certRepo.ListInternalRoots(ctx, 0)
	if err != nil || len(roots) == 0 {
		t.Fatalf("查询内部根 CA 失败: %v", err)
	}
	caRootID := roots[0].CertID
	issuers, err := certRepo.ListIssuerCAs(ctx, 0, caRootID)
	if err != nil || len(issuers) == 0 {
		t.Fatalf("查询内部签发 CA 失败: %v", err)
	}
	caIssuerID := issuers[0].CertID

	// 用户证书只需要"记录类型正确"：吊销逻辑不校验证书本体，
	// 因此直接写入一条 client 记录，避免为权限测试再造一套签发流程。
	clientRec := &repo.CertRecord{
		TenantID: 0, CertType: repo.CertTypeClient, CertName: "perm-test-client-" + suffix,
		SerialNumber: "PERMTEST" + suffix, Fingerprint: "PERMTESTFP" + suffix,
		Status: repo.CertStatusActive, CertSource: repo.CertSourceInternal,
	}
	if err := certRepo.Create(ctx, clientRec); err != nil {
		t.Fatalf("写入测试用用户证书失败: %v", err)
	}
	clientRevoke := "/api/v1/ukey/certs/" + strconv.FormatInt(clientRec.CertID, 10) + "/revoke"
	caRevoke := "/api/v1/ukey/certs/" + strconv.FormatInt(caRootID, 10) + "/revoke"
	caIssuerRevoke := "/api/v1/ukey/certs/" + strconv.FormatInt(caIssuerID, 10) + "/revoke"

	// ---- 权限矩阵 ----
	type step struct {
		role      string
		method    string
		target    string
		forbidden bool
		desc      string
	}
	steps := []step{
		// 系统管理员：用户维度可用，CA 维度不可用
		{"sys_admin", http.MethodGet, "/api/v1/ukey/certs", false, "查看证书列表（任一权限）"},
		{"sys_admin", http.MethodPost, "/api/v1/ukey/certs", false, "登记绑定用户证书"},
		{"sys_admin", http.MethodPost, "/api/v1/ukey/certs/issue", false, "用内部 CA 为用户签发"},
		{"sys_admin", http.MethodPost, "/api/v1/ukey/certs/issue-by-pubkey/challenge", false, "领取持有证明挑战"},
		{"sys_admin", http.MethodPost, "/api/v1/ukey/certs/issue-by-pubkey", false, "按公钥签发"},
		{"sys_admin", http.MethodPost, clientRevoke, false, "吊销用户证书"},
		{"sys_admin", http.MethodGet, "/api/v1/ukey/ca", true, "查看 CA 台账"},
		{"sys_admin", http.MethodPost, "/api/v1/ukey/ca/init", true, "初始化内部 CA"},
		{"sys_admin", http.MethodPost, "/api/v1/ukey/external-ca", true, "导入外部 CA"},
		{"sys_admin", http.MethodPost, caRevoke, true, "吊销 CA"},

		// 安全管理员：CA 维度可用，用户维度只读（放行路径里不含"吊销 CA"，
		// 它放到最后单独执行，避免提前把 CA 置为 revoked 影响后续判定）
		{"sec_admin", http.MethodGet, "/api/v1/ukey/certs", false, "查看证书列表（任一权限）"},
		{"sec_admin", http.MethodGet, "/api/v1/ukey/ca", false, "查看 CA 台账"},
		{"sec_admin", http.MethodPost, "/api/v1/ukey/ca/init", false, "初始化内部 CA"},
		{"sec_admin", http.MethodPost, "/api/v1/ukey/external-ca", false, "导入外部 CA"},
		{"sec_admin", http.MethodPost, "/api/v1/ukey/certs", true, "登记绑定用户证书"},
		{"sec_admin", http.MethodPost, "/api/v1/ukey/certs/issue", true, "用内部 CA 为用户签发"},
		{"sec_admin", http.MethodPost, "/api/v1/ukey/certs/issue-by-pubkey/challenge", true, "领取持有证明挑战"},
		{"sec_admin", http.MethodPost, "/api/v1/ukey/certs/issue-by-pubkey", true, "按公钥签发"},
		{"sec_admin", http.MethodPost, clientRevoke, true, "吊销用户证书（该路由要求 system:user:update）"},

		// 超级管理员：两类都可用（抽样）
		{"super_admin", http.MethodGet, "/api/v1/ukey/certs", false, "查看证书列表"},
		{"super_admin", http.MethodPost, "/api/v1/ukey/certs", false, "登记绑定用户证书"},
		{"super_admin", http.MethodGet, "/api/v1/ukey/ca", false, "查看 CA 台账"},
		{"super_admin", http.MethodPost, "/api/v1/ukey/ca/init", false, "初始化内部 CA"},

		// 运维人员：全部拒绝
		{"operator", http.MethodGet, "/api/v1/ukey/certs", true, "查看证书列表"},
		{"operator", http.MethodPost, "/api/v1/ukey/certs", true, "登记绑定用户证书"},
		{"operator", http.MethodPost, "/api/v1/ukey/certs/issue", true, "用内部 CA 为用户签发"},
		{"operator", http.MethodPost, "/api/v1/ukey/certs/issue-by-pubkey/challenge", true, "领取持有证明挑战"},
		{"operator", http.MethodPost, "/api/v1/ukey/certs/issue-by-pubkey", true, "按公钥签发"},
		{"operator", http.MethodPost, clientRevoke, true, "吊销用户证书"},
		{"operator", http.MethodGet, "/api/v1/ukey/ca", true, "查看 CA 台账"},
		{"operator", http.MethodPost, "/api/v1/ukey/ca/init", true, "初始化内部 CA"},
		{"operator", http.MethodPost, "/api/v1/ukey/external-ca", true, "导入外部 CA"},
	}
	for _, st := range steps {
		code := call(st.method, st.target, st.role)
		if st.forbidden {
			if code != http.StatusForbidden {
				t.Errorf("%s %s %s（%s）：应 403，实际 %d", st.role, st.method, st.target, st.desc, code)
			}
			continue
		}
		// "非 403"即表示权限已通过；业务层可能因缺请求体返回 400，那是预期的
		if code == http.StatusForbidden {
			t.Errorf("%s %s %s（%s）：不应 403（权限应通过），实际 %d", st.role, st.method, st.target, st.desc, code)
		}
	}

	// ---- 审计日志：operator 的越权请求必须留痕，且记录完整候选权限列表 ----
	readLogs := captureLogs(t)
	if code := call(http.MethodGet, "/api/v1/ukey/certs", "operator"); code != http.StatusForbidden {
		t.Fatalf("operator 查看证书列表应 403，实际 %d", code)
	}
	out := readLogs()
	if !strings.Contains(out, "权限校验失败") {
		t.Fatalf("越权请求应写审计日志：%s", out)
	}
	for _, want := range []string{"system:user:update", "key:cert:manage", "username=" + users["operator"].Username} {
		if !strings.Contains(out, want) {
			t.Fatalf("日志应包含 %q：%s", want, out)
		}
	}

	// ---- 吊销 CA 的类型限制：sys_admin 被拒（且留痕），sec_admin 放行 ----
	readLogs2 := captureLogs(t)
	if code := call(http.MethodPost, caIssuerRevoke, "sys_admin"); code != http.StatusForbidden {
		t.Fatalf("sys_admin 吊销 CA 应 403，实际 %d", code)
	}
	if out2 := readLogs2(); !strings.Contains(out2, "权限校验失败") || !strings.Contains(out2, "key:cert:manage") {
		t.Fatalf("吊销 CA 被拒也应写审计日志并记录 key:cert:manage：%s", out2)
	}
	if code := call(http.MethodPost, caIssuerRevoke, "sec_admin"); code == http.StatusForbidden {
		t.Fatalf("sec_admin 持有 key:cert:manage，应能吊销 CA，实际 403")
	}
}
