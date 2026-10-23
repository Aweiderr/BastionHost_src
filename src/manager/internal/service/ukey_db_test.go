package service

import (
	"context"
	stdcrypto "crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	bastioncrypto "bastion.local/shared/crypto"
	"bastion.local/shared/crypto/cert"
	"bastion.local/shared/crypto/soft"
)

// TestUKeyServiceDatabaseFlow 是 UKey 链路的**数据库集成测试**。
//
// 为什么需要它：repo 层有二十多条 SQL，`go build` / `go vet` 都查不出
// "列数不匹配""参数错位""列名拼错"这类问题——它们只在真正连库执行时暴露，
// 而那时通常已经在部署现场了。本测试把 InitCA、证书登记（严格 / 宽松）、
// 挑战下发、登录校验、吊销、列表查询整条链路在真库上跑一遍。
//
// 默认**跳过**：只有显式设置了 BASTION_UKEY_TEST_DSN 才运行，
// 因此日常 `go test ./...` 不受影响。为了不可能误伤生产库，
// 这里额外强制要求 DSN 中的库名包含 `_itest`——写错 DSN 时宁可测试失败，
// 也不能对业务库执行任何语句。
//
// **必须使用独占的干净库**：
//   - 每次运行前重建（测试会删除自己刚建的 RSA CA 记录来模拟"老部署只有 SM2"，
//     并要求 InitCA 首次真的创建 CA）；
//   - 不要与 api 包的权限集成测试共用同一个库——`go test ./...` 会并行跑包，
//     那边会建 CA、吊销 CA、写入测试证书，共库必然互相破坏。
//     权限测试用 `BASTION_UKEY_PERM_TEST_DSN` 指向另一个临时库。
//
// 运行方式（远端）：
//
//	docker exec bastion-pg psql -U bastion -d postgres -c "CREATE DATABASE bastion_itest OWNER bastion"
//	# 依次应用 migrations/*.sql
//	BASTION_UKEY_TEST_DSN='postgres://bastion:.../bastion_itest?sslmode=disable' go test ./internal/service -run UKeyServiceDatabaseFlow -v
func TestUKeyServiceDatabaseFlow(t *testing.T) {
	dsn := os.Getenv("BASTION_UKEY_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 BASTION_UKEY_TEST_DSN，跳过 UKey 数据库集成测试")
	}
	if !strings.Contains(dsn, "_itest") {
		t.Fatalf("拒绝执行：临时库名必须包含 _itest（防止误连业务库），当前 DSN 不含该标记")
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
	configRepo := repo.NewConfigRepo(pool)
	userRepo := repo.NewUserRepo(pool)
	certRepo := repo.NewCertRepo(pool)
	challengeRepo := repo.NewChallengeRepo(pool)
	svc := NewUKeyService(certRepo, challengeRepo, userRepo, configRepo, prov, nil)
	// 操作人先占位，创建测试用户后再指向真实 user_id：
	// create_by 要能被 sys_user 联表解出用户名，测试用户正是为此准备的。
	op := OperatorContext{ClientIP: "127.0.0.1"}

	// UKey 登录在迁移里默认关闭；集成测试要先显式打开。
	if err := configRepo.Set(ctx, 0, cfgKeyUKeyEnabled, "true", 0); err != nil {
		t.Fatalf("启用 UKey 配置失败: %v", err)
	}
	// 本测试会为同一用户登记多张证书，放开上限以免被"数量上限"挡住
	// （上限本身的校验由 5x 段之外的单测与 RegisterCert 的逻辑覆盖）。
	if err := configRepo.Set(ctx, 0, cfgKeyMaxCertsPerUser, "10", 0); err != nil {
		t.Fatalf("放宽证书数量上限失败: %v", err)
	}

	// ---- 测试用户（每次运行用不同用户名，避免与上一轮残留冲突）----
	suffix := time.Now().Format("150405")
	newUser := func(name string) *model.User {
		u := &model.User{
			TenantID: 0, Username: name, DisplayName: name,
			PasswordHash: "itest-hash", PasswordSalt: "itest-salt", PasswordAlgo: "SM3",
			PasswordExpireDays: 90, Status: "active", UserType: "local",
			MFAEnabled: true, MFAType: "ukey",
		}
		if err := userRepo.Create(ctx, u); err != nil {
			t.Fatalf("创建测试用户失败: %v", err)
		}
		return u
	}
	owner := newUser("ukey-itest-" + suffix)
	other := newUser("ukey-itest-other-" + suffix)
	op.UserID, op.Username = owner.UserID, owner.Username

	// ---- 1. 内部 CA 初始化：生成 + 幂等 ----
	info1, err := svc.InitCA(ctx, op)
	if err != nil {
		t.Fatalf("初始化内部 CA 失败: %v", err)
	}
	if !info1.Created || info1.RootCertID == 0 || info1.IssuerCertID == 0 {
		t.Fatalf("首次初始化应新建 CA，实际 %+v", info1)
	}
	info2, err := svc.InitCA(ctx, op)
	if err != nil {
		t.Fatalf("第二次初始化失败: %v", err)
	}
	if info2.Created || info2.RootCertID != info1.RootCertID || info2.IssuerCertID != info1.IssuerCertID {
		t.Fatalf("InitCA 必须幂等，第二次应返回同一套 CA：%+v / %+v", info1, info2)
	}
	// 幂等路径要从库里重建签名句柄（签发 CA 私钥由密码服务托管）
	ca, err := svc.loadInternalCA(ctx, cert.AlgorithmSM2)
	if err != nil {
		t.Fatalf("从库中重建内部 CA 失败: %v", err)
	}
	// ---- 1b. 只补 RSA 的路径（模拟"老部署只有 SM2 链"）----
	//
	// 生产上最常见的形态是：SM2 链早已存在并给用户签过证书，某次要补 RSA 链。
	// 这里先删掉刚建的两张 RSA CA 记录（**仅允许在 _itest 临时库上执行**，
	// 本测试开头已强制校验 DSN 含 _itest）来还原那个起点。
	sm2RootBefore, err := certRepo.GetByID(ctx, info1.RootCertID)
	if err != nil {
		t.Fatalf("读取 SM2 根记录失败: %v", err)
	}
	if _, derr := pool.Exec(ctx, `DELETE FROM km_cert
		WHERE (cert_id = $1 OR cert_id = $2)
		  AND cert_name IN ('内部 RSA 根 CA', '内部 RSA 签发 CA')`,
		info1.RSARootCertID, info1.RSAIssuerCertID); derr != nil {
		t.Fatalf("清理 RSA CA 记录失败: %v", derr)
	}
	infoRSA, err := svc.InitCA(ctx, op)
	if err != nil {
		t.Fatalf("补建 RSA 链失败: %v", err)
	}
	if infoRSA.RSARootCertID == 0 || infoRSA.RSAIssuerCertID == 0 {
		t.Fatal("RSA 链不存在时应被补建")
	}
	if infoRSA.Created {
		t.Fatal("SM2 链已存在，不应被重新生成")
	}
	if infoRSA.RootCertID != info1.RootCertID || infoRSA.IssuerCertID != info1.IssuerCertID {
		t.Fatalf("SM2 链的 cert_id 不应变化：%d/%d → %d/%d",
			info1.RootCertID, info1.IssuerCertID, infoRSA.RootCertID, infoRSA.IssuerCertID)
	}
	sm2RootAfter, err := certRepo.GetByID(ctx, info1.RootCertID)
	if err != nil {
		t.Fatalf("回读 SM2 根失败: %v", err)
	}
	if sm2RootAfter.SerialNumber != sm2RootBefore.SerialNumber {
		t.Fatalf("SM2 根序列号不应变化：%s → %s",
			sm2RootBefore.SerialNumber, sm2RootAfter.SerialNumber)
	}
	// 两条链都已存在时再调一次：必须完全幂等
	infoAgain, err := svc.InitCA(ctx, op)
	if err != nil {
		t.Fatalf("第三次初始化失败: %v", err)
	}
	if infoAgain.Created || infoAgain.RSACreated {
		t.Fatal("两条链都已存在时不应再创建任何 CA")
	}
	// 添加人（create_by）必须真的写进去、并且能被 sys_user 联表解出用户名：
	// 前端证书链列表的「添加人」列就靠这两个字段，任一环节缺失该列都是空的。
	rootRec, err := certRepo.GetByID(ctx, info1.RootCertID)
	if err != nil {
		t.Fatalf("查询内部根 CA 记录失败: %v", err)
	}
	if rootRec.CreateBy == nil || *rootRec.CreateBy != owner.UserID {
		t.Fatalf("内部根 CA 必须记录添加人 create_by=%d，实际 %v", owner.UserID, rootRec.CreateBy)
	}
	if rootRec.CreatorName != owner.Username {
		t.Fatalf("添加人用户名应由 sys_user 联表得到 %q，实际 %q", owner.Username, rootRec.CreatorName)
	}
	issuerRec, err := certRepo.GetByID(ctx, info1.IssuerCertID)
	if err != nil {
		t.Fatalf("查询内部签发 CA 记录失败: %v", err)
	}
	if issuerRec.CreateBy == nil || issuerRec.CreatorName != owner.Username {
		t.Fatalf("内部签发 CA 的添加人缺失：create_by=%v creator_name=%q",
			issuerRec.CreateBy, issuerRec.CreatorName)
	}

	// ---- 2. 内部证书登记（严格路径）----
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成客户端密钥失败: %v", err)
	}
	issued, err := cert.IssueUserCert(ca.issuer, cert.UserCertRequest{
		DN:           cert.DN{Country: "CN", Organization: "itest", CommonName: owner.Username},
		ValidityDays: 30,
		PublicKey:    &priv.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发内部用户证书失败: %v", err)
	}
	rec, err := svc.RegisterCert(ctx, RegisterCertRequest{
		UserID: owner.UserID, CertPEM: string(issued.CertPEM), CertSource: repo.CertSourceInternal,
	}, op)
	if err != nil {
		t.Fatalf("登记内部证书失败: %v", err)
	}
	if rec.UnverifiedChain {
		t.Fatal("内部 CA 签发的证书不应被标记为「未验证链」")
	}
	if rec.ChainID == nil {
		t.Fatal("内部证书应记录签发 CA 的 cert_id")
	}
	// 序列号必须在入库前归一化
	if rec.SerialNumber != issued.Identity.SerialNumber || rec.SerialNumber == "" {
		t.Fatalf("序列号未按归一化形式落库：%q / %q", rec.SerialNumber, issued.Identity.SerialNumber)
	}

	// 幂等：同一张证书重复登记返回同一条记录
	recAgain, err := svc.RegisterCert(ctx, RegisterCertRequest{
		UserID: owner.UserID, CertPEM: string(issued.CertPEM), CertSource: repo.CertSourceInternal,
	}, op)
	if err != nil {
		t.Fatalf("重复登记应幂等: %v", err)
	}
	if recAgain.CertID != rec.CertID {
		t.Fatalf("重复登记应返回已有记录（%d），实际新建了 %d", rec.CertID, recAgain.CertID)
	}

	// 抢注：同一张证书登记到另一个用户必须被拒
	if _, err := svc.RegisterCert(ctx, RegisterCertRequest{
		UserID: other.UserID, CertPEM: string(issued.CertPEM), CertSource: repo.CertSourceInternal,
	}, op); err == nil {
		t.Fatal("同一张证书绑定到其它用户必须被拒绝（防抢注）")
	}

	// ---- 3. UKey 登录全链路 ----
	signFor := func(t *testing.T, priv *sm2.PrivateKey, srcData string) string {
		t.Helper()
		sig, serr := sm2.SignASN1(rand.Reader, priv, []byte(srcData), sm2.DefaultSM2SignerOpts)
		if serr != nil {
			t.Fatalf("签名失败: %v", serr)
		}
		return base64.StdEncoding.EncodeToString(sig)
	}
	login := func(t *testing.T, u *model.User, certPEM string, priv *sm2.PrivateKey) error {
		t.Helper()
		ch, cerr := svc.IssueChallenge(ctx, u, "10.0.0.9", "itest-agent")
		if cerr != nil {
			t.Fatalf("下发挑战失败: %v", cerr)
		}
		_, _, verr := svc.VerifyLogin(ctx, UKeyLoginRequest{
			ChallengeID: ch.ChallengeID, Cert: certPEM,
			SrcData: ch.SrcData, Signature: signFor(t, priv, ch.SrcData),
		}, "10.0.0.9")
		return verr
	}

	if err := login(t, owner, string(issued.CertPEM), priv); err != nil {
		t.Fatalf("内部证书登录应成功: %v", err)
	}

	// 挑战一次性：同一 challenge_id 再提交一次必须失败
	ch, err := svc.IssueChallenge(ctx, owner, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发挑战失败: %v", err)
	}
	req := UKeyLoginRequest{
		ChallengeID: ch.ChallengeID, Cert: string(issued.CertPEM),
		SrcData: ch.SrcData, Signature: signFor(t, priv, ch.SrcData),
	}
	if _, _, err := svc.VerifyLogin(ctx, req, "10.0.0.9"); err != nil {
		t.Fatalf("首次校验应成功: %v", err)
	}
	if _, _, err := svc.VerifyLogin(ctx, req, "10.0.0.9"); err == nil {
		t.Fatal("挑战是一次性的，第二次提交必须失败（防重放）")
	}

	// 来源 IP 不一致必须拒绝
	chIP, err := svc.IssueChallenge(ctx, owner, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发挑战失败: %v", err)
	}
	if _, _, err := svc.VerifyLogin(ctx, UKeyLoginRequest{
		ChallengeID: chIP.ChallengeID, Cert: string(issued.CertPEM),
		SrcData: chIP.SrcData, Signature: signFor(t, priv, chIP.SrcData),
	}, "10.0.0.10"); !errors.Is(err, ErrUKeyIPMismatch) {
		t.Fatalf("来源 IP 不一致应拒绝，实际 %v", err)
	}

	// ---- 4. 吊销后必须登录失败 ----
	if _, err := svc.RevokeCert(ctx, rec.CertID, "集成测试吊销", op); err != nil {
		t.Fatalf("吊销证书失败: %v", err)
	}
	if err := login(t, owner, string(issued.CertPEM), priv); !errors.Is(err, ErrUKeyLoginRejected) {
		t.Fatalf("已吊销证书必须拒绝登录，实际 %v", err)
	}

	// ---- 5. 宽松登记：外部 CA 签发、根证书不可得 ----
	extRoot, err := cert.NewRootCA(ctx, prov, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "外部单位", CommonName: "外部自签根 CA"},
		ValidityDays: 3650, MaxPathLen: 1,
	})
	if err != nil {
		t.Fatalf("生成外部根 CA 失败: %v", err)
	}
	extIssuer, err := cert.NewIntermediateCA(ctx, prov, extRoot, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "外部单位", CommonName: "外部签发 CA"},
		ValidityDays: 1825, MaxPathLen: 0,
	})
	if err != nil {
		t.Fatalf("生成外部签发 CA 失败: %v", err)
	}
	extPriv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成外部客户端密钥失败: %v", err)
	}
	extIssued, err := cert.IssueUserCert(extIssuer, cert.UserCertRequest{
		DN:           cert.DN{Country: "CN", Organization: "外部单位", CommonName: "超管"},
		ValidityDays: 30,
		PublicKey:    &extPriv.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发外部用户证书失败: %v", err)
	}
	extReq := RegisterCertRequest{
		UserID: owner.UserID, CertPEM: string(extIssued.CertPEM), CertSource: repo.CertSourceExternal,
	}

	// 5a. 默认（require_internal_ca=true）：无信任锚必须拒绝
	if err := configRepo.Set(ctx, 0, cfgKeyRequireInternalCA, "true", 0); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	if _, err := svc.RegisterCert(ctx, extReq, op); err == nil {
		t.Fatal("默认配置下，无信任锚的外部证书必须被拒绝")
	}

	// 5b. 关闭 require_internal_ca：允许登记，但必须被标记为「未验证链」
	if err := configRepo.Set(ctx, 0, cfgKeyRequireInternalCA, "false", 0); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	extRec, err := svc.RegisterCert(ctx, extReq, op)
	if err != nil {
		t.Fatalf("宽松模式下外部证书应可登记: %v", err)
	}
	if !extRec.UnverifiedChain {
		t.Fatal("宽松登记的证书必须被标记为「未验证链」，否则事后无法区分")
	}
	if !strings.HasPrefix(extRec.Remark, repo.CertRemarkUnverifiedChain) {
		t.Fatalf("remark 应带未验证链标记，实际 %q", extRec.Remark)
	}
	if extRec.ChainID != nil {
		t.Fatal("宽松登记没有可用的签发 CA 记录，chain_id 应为空")
	}
	// 从列表读回来时标记必须仍然可见（接口可观测性）
	list, _, err := svc.ListCerts(ctx, repo.CertFilter{Keyword: extRec.SerialNumber})
	if err != nil {
		t.Fatalf("查询证书列表失败: %v", err)
	}
	found := false
	for _, item := range list {
		if item.CertID == extRec.CertID {
			found = true
			if !item.UnverifiedChain {
				t.Fatal("列表返回的记录也必须带「未验证链」标记")
			}
		}
	}
	if !found {
		t.Fatalf("按序列号 %s 未在列表中查到刚登记的证书", extRec.SerialNumber)
	}

	// 5c. 宽松登记 ≠ 放行登录：拿另一把私钥签名必须失败
	stranger, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	if err := login(t, owner, string(extIssued.CertPEM), stranger); !errors.Is(err, ErrUKeyLoginRejected) {
		t.Fatalf("无对应私钥者即使证书已宽松登记也必须登录失败，实际 %v", err)
	}

	// 5d. 持钥者可以登录（宽松登记的意义所在）
	if err := login(t, owner, string(extIssued.CertPEM), extPriv); err != nil {
		t.Fatalf("持钥者用宽松登记的证书登录应成功: %v", err)
	}

	// 5e. internal 来源在宽松模式下仍然严格：外部证书不能按 internal 登记
	extPriv2, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	extIssued2, err := cert.IssueUserCert(extIssuer, cert.UserCertRequest{
		DN:           cert.DN{CommonName: "超管2"},
		ValidityDays: 30,
		PublicKey:    &extPriv2.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发外部用户证书失败: %v", err)
	}
	if _, err := svc.RegisterCert(ctx, RegisterCertRequest{
		UserID: owner.UserID, CertPEM: string(extIssued2.CertPEM), CertSource: repo.CertSourceInternal,
	}, op); err == nil {
		t.Fatal("cert_source=internal 的证书必须能链到内部 CA，宽松模式不得绕过")
	}

	// ---- 6. 列表与 CA 查询 ----
	cas, err := svc.ListCACerts(ctx)
	if err != nil {
		t.Fatalf("查询 CA 列表失败: %v", err)
	}
	if len(cas) < 2 {
		t.Fatalf("CA 列表应至少包含内部根与内部签发 CA，实际 %d 张", len(cas))
	}
	caInfo, ok, err := svc.CAInfo(ctx)
	if err != nil || !ok || caInfo.RootCertID != info1.RootCertID {
		t.Fatalf("CAInfo 应返回刚初始化的内部 CA：ok=%v info=%+v err=%v", ok, caInfo, err)
	}
	if _, total, err := svc.ListCerts(ctx, repo.CertFilter{}); err != nil || total < 2 {
		t.Fatalf("证书列表应至少包含内部与外部两张，实际 total=%d err=%v", total, err)
	}

	// ---- 7. 外部 CA 导入后，同类证书走"已验证链"路径 ----
	if _, err := svc.ImportExternalCA(ctx, string(extRoot.CertPEM), op); err != nil {
		t.Fatalf("导入外部 CA 失败: %v", err)
	}
	if _, err := svc.ImportExternalCA(ctx, string(extRoot.CertPEM), op); err != nil {
		t.Fatalf("重复导入外部 CA 应幂等: %v", err)
	}
	extPriv3, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	extIssued3, err := cert.IssueUserCert(extIssuer, cert.UserCertRequest{
		DN:           cert.DN{CommonName: "超管3"},
		ValidityDays: 30,
		PublicKey:    &extPriv3.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发外部用户证书失败: %v", err)
	}
	// 提交"用户证书 + 中间 CA"完整链，走 external 的链校验分支
	fullChain := append(append([]byte{}, extIssued3.CertPEM...), extIssuer.CertPEM...)
	verifiedRec, err := svc.RegisterCert(ctx, RegisterCertRequest{
		UserID: owner.UserID, CertPEM: string(fullChain), CertSource: repo.CertSourceExternal,
	}, op)
	if err != nil {
		t.Fatalf("导入信任锚后，外部证书应能通过链校验: %v", err)
	}
	if verifiedRec.UnverifiedChain {
		t.Fatal("链校验通过的外部证书不应被标记为「未验证链」")
	}
	if err := login(t, owner, string(extIssued3.CertPEM), extPriv3); err != nil {
		t.Fatalf("链校验通过的外部证书登录应成功: %v", err)
	}

	// ---- 8. 公钥 + 持有证明签发（真机灌卡的可行路径）----
	//
	// 实测的 UKey 中间件没有 CSR 接口，只能把 Key 里的公钥交给服务端、
	// 由内部 CA 签一张新证书再灌回 Key。安全前提是**持有证明**。
	ukeyPriv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 UKey 公钥对失败: %v", err)
	}
	chIssue, err := svc.IssueCertIssueChallenge(ctx, owner.UserID, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发签发用挑战失败: %v", err)
	}
	spki, err := smx509.MarshalPKIXPublicKey(&ukeyPriv.PublicKey)
	if err != nil {
		t.Fatalf("编码 SPKI 公钥失败: %v", err)
	}
	issuedByPub, err := svc.IssueUserCertByPublicKey(ctx, IssueByPublicKeyRequest{
		UserID: owner.UserID, ChallengeID: chIssue.ChallengeID,
		PublicKey:  base64.StdEncoding.EncodeToString(spki),
		SrcData:    chIssue.SrcData,
		Signature:  signFor(t, ukeyPriv, chIssue.SrcData),
		CommonName: "ukey-lab-user",
	}, "10.0.0.9", op)
	if err != nil {
		t.Fatalf("凭持有证明签发证书应成功: %v", err)
	}
	if issuedByPub.CertPEM == "" {
		t.Fatal("必须返回 cert_pem，前端要把它灌入 UKey")
	}
	if issuedByPub.CertSource != repo.CertSourceInternal || issuedByPub.ChainID == nil {
		t.Fatalf("按公钥签发的证书应由内部 CA 签发：source=%s chain_id=%v",
			issuedByPub.CertSource, issuedByPub.ChainID)
	}
	issuedParsed, err := cert.ParsePEM([]byte(issuedByPub.CertPEM))
	if err != nil {
		t.Fatalf("签发出的证书应可解析: %v", err)
	}
	if !strings.Contains(issuedParsed.Subject.String(), "ukey-lab-user") {
		t.Fatalf("证书 CN 应为 ukey-lab-user，实际 %s", issuedParsed.Subject.String())
	}
	// 灌卡之后：用新证书 + Key 内私钥登录（这就是真机验证过的链路）
	if err := login(t, owner, issuedByPub.CertPEM, ukeyPriv); err != nil {
		t.Fatalf("按公钥签发的证书应能完成 UKey 登录: %v", err)
	}

	// 负例：持有证明由别人的私钥签署 → 必须拒绝签发
	chIssue2, err := svc.IssueCertIssueChallenge(ctx, owner.UserID, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发签发用挑战失败: %v", err)
	}
	if _, err := svc.IssueUserCertByPublicKey(ctx, IssueByPublicKeyRequest{
		UserID: owner.UserID, ChallengeID: chIssue2.ChallengeID,
		PublicKey: base64.StdEncoding.EncodeToString(spki),
		SrcData:   chIssue2.SrcData,
		Signature: signFor(t, stranger, chIssue2.SrcData),
	}, "10.0.0.9", op); err == nil {
		t.Fatal("持有证明验签失败时必须拒绝签发（否则等于抄一个公钥就能换证书）")
	}

	// 负例：挑战与目标用户不一致 → 必须拒绝
	chIssue3, err := svc.IssueCertIssueChallenge(ctx, owner.UserID, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发签发用挑战失败: %v", err)
	}
	if _, err := svc.IssueUserCertByPublicKey(ctx, IssueByPublicKeyRequest{
		UserID: other.UserID, ChallengeID: chIssue3.ChallengeID,
		PublicKey: base64.StdEncoding.EncodeToString(spki),
		SrcData:   chIssue3.SrcData,
		Signature: signFor(t, ukeyPriv, chIssue3.SrcData),
	}, "10.0.0.9", op); err == nil {
		t.Fatal("挑战属于 A 用户、却给 B 用户签发证书，必须拒绝")
	}

	// ---- 9. RSA 兼容链：登记 + 登录（对应真机中间件的 RSA-SHA1）----
	//
	// 中间件的算法常量只有 RSA-MD5 / RSA-SHA1 / SM2-SM3：RSA 型 UKey
	// 能产出的签名只可能是 SHA-1，因此这条路径必须真的能走通。
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成 RSA 客户端密钥失败: %v", err)
	}
	rsaChain, err := svc.loadInternalCA(ctx, cert.AlgorithmRSA)
	if err != nil {
		t.Fatalf("加载 RSA 内部 CA 失败: %v", err)
	}
	rsaIssued, err := cert.IssueUserCert(rsaChain.issuer, cert.UserCertRequest{
		DN:           cert.DN{Country: "CN", Organization: "itest", CommonName: "rsa-ukey"},
		ValidityDays: 30,
		PublicKey:    &rsaPriv.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发 RSA 用户证书失败: %v", err)
	}
	rsaRec, err := svc.RegisterCert(ctx, RegisterCertRequest{
		UserID: owner.UserID, CertPEM: string(rsaIssued.CertPEM), CertSource: repo.CertSourceInternal,
	}, op)
	if err != nil {
		t.Fatalf("登记 RSA 证书失败（RSA 证书必须能走 internal 链）: %v", err)
	}
	if rsaRec.ChainID == nil {
		t.Fatal("RSA 证书应记录签发 CA 的 cert_id")
	}
	if rsaRec.UnverifiedChain {
		t.Fatal("RSA 内部证书不应被标记为未验证链")
	}

	loginRSA := func(t *testing.T, h stdcrypto.Hash) error {
		t.Helper()
		ch, cerr := svc.IssueChallenge(ctx, owner, "10.0.0.9", "itest-agent")
		if cerr != nil {
			t.Fatalf("下发挑战失败: %v", cerr)
		}
		d := h.New()
		d.Write([]byte(ch.SrcData))
		sig, serr := rsa.SignPKCS1v15(rand.Reader, rsaPriv, h, d.Sum(nil))
		if serr != nil {
			t.Fatalf("RSA 签名失败: %v", serr)
		}
		_, _, verr := svc.VerifyLogin(ctx, UKeyLoginRequest{
			ChallengeID: ch.ChallengeID, Cert: string(rsaIssued.CertPEM),
			SrcData: ch.SrcData, Signature: base64.StdEncoding.EncodeToString(sig),
		}, "10.0.0.9")
		return verr
	}
	// SHA-1：真机中间件唯一可用的 RSA 摘要
	if err := loginRSA(t, stdcrypto.SHA1); err != nil {
		t.Fatalf("RSA-SHA1 登录应通过（真机中间件的硬约束）: %v", err)
	}
	// SHA-256：新中间件/新设备
	if err := loginRSA(t, stdcrypto.SHA256); err != nil {
		t.Fatalf("RSA-SHA256 登录应通过: %v", err)
	}
	// 负例：别人私钥签名 → 必须拒绝
	strangerRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	chRSA, err := svc.IssueChallenge(ctx, owner, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发挑战失败: %v", err)
	}
	badDigest := stdcrypto.SHA1.New()
	badDigest.Write([]byte(chRSA.SrcData))
	badSig, err := rsa.SignPKCS1v15(rand.Reader, strangerRSA, stdcrypto.SHA1, badDigest.Sum(nil))
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	if _, _, err := svc.VerifyLogin(ctx, UKeyLoginRequest{
		ChallengeID: chRSA.ChallengeID, Cert: string(rsaIssued.CertPEM),
		SrcData: chRSA.SrcData, Signature: base64.StdEncoding.EncodeToString(badSig),
	}, "10.0.0.9"); !errors.Is(err, ErrUKeyLoginRejected) {
		t.Fatalf("RSA 证书配错误的私钥必须被拒，实际 %v", err)
	}
	// 负例：跨算法签发（RSA 链签 SM2 公钥）必须被拒
	sm2Priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 SM2 密钥失败: %v", err)
	}
	if _, err := cert.IssueUserCert(rsaChain.issuer, cert.UserCertRequest{
		DN: cert.DN{CommonName: "cross"}, ValidityDays: 30, PublicKey: &sm2Priv.PublicKey,
	}); err == nil {
		t.Fatal("用 RSA 签发 CA 签 SM2 公钥必须被拒绝")
	}

	// ---- 10. 挑战取回：只认 challenge_id，且三种拒绝能区分 ----
	//
	// 这条路径**不涉及任何口令**（账密分支已移除，口令校验只剩 /auth/login），
	// 因此这里不构造任何用户名/口令输入，只验证挑战本身的状态机。
	ch10, err := svc.IssueChallenge(ctx, owner, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发挑战失败: %v", err)
	}
	got10, err := svc.Challenge(ctx, ch10.ChallengeID)
	if err != nil {
		t.Fatalf("按 challenge_id 取回挑战应成功: %v", err)
	}
	if got10.SignData != ch10.SignData || got10.SrcData != ch10.SrcData {
		t.Fatal("取回的签名原文与 src_data 必须与下发时完全一致")
	}

	// 不存在
	if _, err := svc.Challenge(ctx, "no-such-challenge-id"); !errors.Is(err, ErrUKeyChallengeInvalid) {
		t.Fatalf("不存在的挑战应为 ErrUKeyChallengeInvalid，实际 %v", err)
	}
	// 已过期：把库中的到期时间改到过去（仅临时库）
	if _, derr := pool.Exec(ctx,
		`UPDATE sys_login_challenge SET expire_time = now() - interval '1 minute' WHERE challenge_id = $1`,
		ch10.ChallengeID); derr != nil {
		t.Fatalf("构造过期挑战失败: %v", derr)
	}
	if _, err := svc.Challenge(ctx, ch10.ChallengeID); !errors.Is(err, ErrUKeyChallengeExpired) {
		t.Fatalf("过期挑战应为 ErrUKeyChallengeExpired，实际 %v", err)
	}
	// 已消费
	ch11, err := svc.IssueChallenge(ctx, owner, "10.0.0.9", "itest-agent")
	if err != nil {
		t.Fatalf("下发挑战失败: %v", err)
	}
	if err := svc.challenges.ConsumeUKey(ctx, ch11.ChallengeID, 0); err != nil {
		t.Fatalf("消费挑战失败: %v", err)
	}
	if _, err := svc.Challenge(ctx, ch11.ChallengeID); !errors.Is(err, ErrUKeyChallengeConsumed) {
		t.Fatalf("已消费挑战应为 ErrUKeyChallengeConsumed，实际 %v", err)
	}
}
