// Command manager 是堡垒机管理服务入口。
//
// 管理服务承担管理面职责：初始化、身份认证、用户与角色、资产与账号、
// 策略配置、工单审批、报表与租户治理（对应《堡垒机系统规划文档》管理服务层）。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/api"
	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/netcfg"
	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/manager/internal/store"
	"bastion.local/shared/auth"
	"bastion.local/shared/captcha"
	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/factory"
	"bastion.local/shared/sms"
)

// managerNodeID 生成管理节点的集群标识。
//
// 用主机名而不是固定 "manager-1"：双机 / 多活部署下多个管理节点
// 若共用同一标识，后启动的会把先启动的覆盖掉，集群界面上就只剩一个节点，
// 而这恰恰掩盖了"到底部署了几个管理节点"这个最该看出来的事实。
func managerNodeID() string {
	if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
		return "manager-" + h
	}
	return "manager-1"
}

// localIPv4 枚举本机非回环 IPv4 地址（与 server 端 nodebeat 的实现同源）。
//
// 上报列表而不是单个地址：设备常有多网卡，运维录入的可能是任意一个。
func localIPv4() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
}

// managerAddress 生成管理节点对外可读的地址（主机名 + 监听端口）。
//
// 监听地址是 ":8080" 这种"所有网卡"的形式，单看它运维不知道节点在哪台机器上。
func managerAddress(listen string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		return host + listen[i:]
	}
	return host
}

// runManagerHeartbeat 让管理节点在集群中保持可见。
//
// 只上报"我还在"，管理节点不承载运维会话，因此负载字段留空。
// 选主与仲裁交给部署层（数据库主备、容器编排）—— 这里不做第二套仲裁：
// 两套心跳只会让人搞不清到底谁说了算。
func runManagerHeartbeat(ctx context.Context, svc *service.NodeService, cfg *config.Config) {
	id := managerNodeID()
	failStreak := 0
	tick := func() {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if _, err := svc.Heartbeat(cctx, repo.HeartbeatInput{
			NodeID:   id,
			NodeName: id,
			NodeRole: model.NodeRoleManager,
			Address:  managerAddress(cfg.HTTPAddr),
			Version:  config.Version,
			LocalIPs: localIPv4(),
		}); err != nil {
			failStreak++
			// 只在前几次与约每分钟打一条：数据库或迁移未就绪时
			// 每 3 秒一条日志会把日志刷满
			if failStreak <= 3 || failStreak%20 == 0 {
				slog.Warn("管理节点心跳失败", "err", err, "fail_streak", failStreak)
			}
			return
		}
		failStreak = 0
	}
	tick()
	ticker := time.NewTicker(svc.HeartbeatInterval(ctx))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---------- 密码服务（工厂模式）----------
	prov, err := factory.New(factory.Config{
		Provider:      cfg.Crypto.Provider,
		Backend:       cfg.Crypto.Backend,
		KeyStoreDir:   cfg.Crypto.KeyStoreDir,
		MasterKeyFile: cfg.Crypto.MasterKeyFile,
		Pkcs11LibPath: cfg.Crypto.Pkcs11LibPath,
		Pkcs11Pin:     cfg.Crypto.Pkcs11Pin,
		Pkcs11Slot:    cfg.Crypto.Pkcs11Slot,
	})
	if err != nil {
		slog.Error("密码服务初始化失败", "err", err,
			"provider", cfg.Crypto.Provider, "backend", cfg.Crypto.Backend)
		os.Exit(1)
	}
	slog.Info("密码服务已就绪",
		"provider", prov.Name(), "backend", prov.Backend(), "desc", prov.Description())

	// ---------- 数据库 ----------
	pool := connectDB(ctx, cfg)
	var (
		userRepo     *repo.UserRepo
		roleRepo     *repo.RoleRepo
		configRepo   *repo.ConfigRepo
		initRepo     *repo.InitRepo
		keyRepo      *repo.KeyRepo
		assetRepo    *repo.AssetRepo
		accountRepo  *repo.AccountRepo
		oplogRepo    *repo.OperationLogRepo
		policyRepo   *repo.PolicyRepo
		ticketRepo   *repo.TicketRepo
		grantRepo    *repo.GrantRepo
		certRepo     *repo.CertRepo
		sessionRepo  *repo.SessionRepo
		setupSvc     *service.SetupService
		authSvc      *service.AuthService
		assetSvc     *service.AssetService
		userSvc      *service.UserService
		policySvc    *service.PolicyService
		ticketSvc    *service.TicketService
		opsSvc       *service.OpsService
		sessionSvc   *service.SessionService
		sessionKiller *service.SessionKiller
		smsSvc       *service.SmsService
		captchaStore *captcha.Store
		selftestSvc  *service.SelftestService
		networkSvc   *netcfg.NetworkService
		nodeSvc      *service.NodeService
		clusterSvc   *service.ClusterService
		grantSvc     *service.GrantService
		ukeySvc      *service.UKeyService
	)
	if pool != nil {
		defer pool.Close()
		if cfg.DB.AutoMigrate {
			if err := autoMigrate(ctx, cfg, pool); err != nil {
				slog.Error("自动迁移失败", "err", err)
				if cfg.DB.Required {
					os.Exit(1)
				}
			}
		}
		// 仓储层
		userRepo = repo.NewUserRepo(pool)
		roleRepo = repo.NewRoleRepo(pool)
		configRepo = repo.NewConfigRepo(pool)
		initRepo = repo.NewInitRepo(pool)
		keyRepo = repo.NewKeyRepo(pool)
		assetRepo = repo.NewAssetRepo(pool)
		accountRepo = repo.NewAccountRepo(pool)
		oplogRepo = repo.NewOperationLogRepo(pool)
		policyRepo = repo.NewPolicyRepo(pool)
		ticketRepo = repo.NewTicketRepo(pool)
		grantRepo = repo.NewGrantRepo(pool)
		certRepo = repo.NewCertRepo(pool)
		sessionRepo = repo.NewSessionRepo(pool)

		// 令牌管理器（HMAC-SM3）
		tm, err := auth.NewTokenManager(cfg.Auth.JWTSecret)
		if err != nil {
			slog.Error("令牌管理器初始化失败", "err", err)
			os.Exit(1)
		}

		// 密码服务辅助层（业务密钥托管）
		cryptoHelper := service.NewCryptoHelper(prov, configRepo, keyRepo)

		// 业务服务
		setupSvc = service.NewSetupService(userRepo, roleRepo, configRepo, initRepo, prov, tm, cfg)
		authSvc = service.NewAuthService(userRepo, roleRepo, configRepo, tm, cfg, cryptoHelper)
		assetSvc = service.NewAssetService(assetRepo, repo.NewAssetGroupRepo(pool), accountRepo, cryptoHelper, oplogRepo)
		userSvc = service.NewUserService(userRepo, roleRepo, oplogRepo, authSvc)
		policySvc = service.NewPolicyService(policyRepo, configRepo, oplogRepo)
		ticketSvc = service.NewTicketService(ticketRepo, grantRepo, assetRepo, accountRepo, configRepo, oplogRepo)
		// 运维面：服务端（协议代理）经服务间接口调用，凭据解密只在这里发生
		opsSvc = service.NewOpsService(authSvc, grantRepo, assetRepo, accountRepo, policyRepo, oplogRepo, cryptoHelper)
		sessionSvc = service.NewSessionService(sessionRepo, assetRepo, accountRepo, oplogRepo)
		sessionSvc.SetAlertRepo(repo.NewAlertRepo(pool))

		// 短信验证码：后端由环境变量决定，测试与生产的差异只在这里体现。
		//
		// BASTION_SMS_PROVIDER = mock（默认，不真正下发）/ bypass（跳过校验，仅开发）
		//                      / gateway / aliyun / tencent
		// BASTION_ENV          = dev / test / prod
		//
		// 注意：provider=bypass 且 BASTION_ENV=prod 时 sms.New 会返回错误，
		// 管理服务将**拒绝启动** —— 宁可起不来，也不能对外提供可绕过的认证。
		smsProvider, smsErr := sms.New(sms.Config{
			Provider:        os.Getenv("BASTION_SMS_PROVIDER"),
			Env:             os.Getenv("BASTION_ENV"),
			Debug:           cfg.Debug,
			FixedCode:       os.Getenv("BASTION_SMS_FIXED_CODE"),
			GatewayURL:      os.Getenv("BASTION_SMS_GATEWAY_URL"),
			GatewayToken:    os.Getenv("BASTION_SMS_GATEWAY_TOKEN"),
			GatewayTemplate: os.Getenv("BASTION_SMS_TEMPLATE"),
		})
		if smsErr != nil {
			slog.Error("短信服务装配失败", "err", smsErr)
			os.Exit(1)
		}
		smsSvc = service.NewSmsService(repo.NewSmsCodeRepo(pool), userRepo, configRepo, smsProvider,
			os.Getenv("BASTION_SMS_FIXED_CODE"))
		authSvc.SetSmsService(smsSvc)
		smsSvc.StartCleanupLoop(ctx, time.Hour, 7)
		// 登录挑战仓储在认证服务与 UKey 服务之间共享（同一个连接池上的无状态仓储）
		challengeRepo := repo.NewChallengeRepo(pool)
		authSvc.SetChallengeRepo(challengeRepo)

		// 图形验证码：按连续登录失败次数触发，不必每次登录都填。
		// 存在进程内存（量大、时效短，入库写放大不划算）；代价是多实例不共享，
		// 将来横向扩展时替换 captcha.Store 的实现即可。
		captchaStore = captcha.NewStore(0)
		authSvc.SetCaptchaStore(captchaStore)

		// 登录日志：audit_login_log 建表后一直没有写入方，
		// "谁从哪登录、成功还是失败"完全没有留痕。装配写入方。
		loginLogRepo := repo.NewLoginLogRepo(pool)
		authSvc.SetLoginLogRepo(loginLogRepo)

		// 设备自检：密码服务 + 软件完整性，四种触发时机
		selftestSvc = service.NewSelftestService(repo.NewSelftestRepo(pool), configRepo, prov)
		// 完整性基线必须在**接受请求之前**采样：否则第一次自检会把
		// "当时的文件"当作基准，从而发现不了启动后发生的替换
		service.InitBootDigest(prov)

		// 网络配置变更：暂存 + 倒计时确认 + 自动回滚。
		// 确认窗口默认 60 秒 —— 足够运维切个终端验证连通性，
		// 又不至于让"忘记确认"变成长时间的网络中断。
		networkSvc = netcfg.NewNetworkService(60 * time.Second)

		// 集群节点：代理节点的注册、心跳与运维动作（停用 / 摘除 / 调权重）。
		//
		// 这里**不需要**任何后台巡检协程来把节点标记为离线：
		// "在线"由最后心跳时间实时推导，一旦落库就需要有人负责改状态，
		// 而那个"有人"恰恰是已经挂掉的节点。
		// 集群：运维录入的设备分组（集群名 + 设备 IP），由管理服务主动探测。
		//
		// 探测放在管理服务而不是让设备上报：集群成员是运维"点出来"的，
		// 管理服务必须能独立回答"我记的这几台现在通不通"，
		// 否则一台设备没装/没启动服务时，界面上只会静静地什么都不显示。
		clusterRepo := repo.NewClusterRepo(pool)
		clusterSvc = service.NewClusterService(clusterRepo, configRepo)
		clusterSvc.StartProbe(ctx)

		// 节点自注册仍保留：它负责上报进程负载，并按设备 IP 取回
		// "是否允许接收新会话"（集群页把某台置为维护后，由这里生效）
		nodeSvc = service.NewNodeService(repo.NewNodeRepo(pool), clusterRepo, configRepo)
		// 在途会话清单只活在内存里：它是节点这一拍心跳的运行事实，
		// 落库会在管理服务重启后变成过期结论。巡检启动前会先等心跳重新报上来。
		liveSessions := service.NewLiveSessionRegistry()
		nodeSvc.SetLiveSessions(liveSessions)

		// 僵尸会话巡检有两条路径：
		//   - 节点心跳超时：进程被强杀（SIGKILL / OOM / 断电），来不及归档；
		//   - 节点在线但在途清单里没有这条会话：优雅重启漏了 finish、
		//     结束上报失败，或连接已经不在了。正常还活着的会话在清单里，不会被收掉。
		recon := service.NewSessionReconciler(sessionRepo, configRepo)
		recon.SetLiveSessions(liveSessions)
		recon.Start(ctx)

		// 访问授权（acl:grant:*）：安全管理员给运维人员直接分配资产。
		// 这组权限点与 acl_grant / acl_grant_rule / acl_grant_log 三张表
		// 从第一版就在，但此前**没有任何配置入口** —— 授权只能由工单审批
		// 间接产生，长期固定的访问关系也得每次走申请审批（见 service/grant.go）。
		grantSvc = service.NewGrantService(grantRepo, userRepo, assetRepo, accountRepo, oplogRepo)
		// 过期授权需要有人把 status 标成 expired：判定层靠 valid_to 兜得住，
		// 但列表里会一直显示"生效中"，界面与实际可用性不符。
		grantSvc.StartExpireWorker(ctx)

		// UKey（智能密码钥匙）证书认证：内部两级 CA + 证书登记 + 签名型第二因子。
		//
		// 与短信 MFA 并列的第二因子实现，共用 sys_login_challenge 的一次性、
		// 短时效、绑定来源 IP 机制。CA 私钥由密码服务（prov）托管，
		// 本进程只持有 key_id —— 数据库被脱库不等于 CA 沦陷。
		ukeySvc = service.NewUKeyService(certRepo, challengeRepo, userRepo, configRepo, prov, oplogRepo)
		ukeySvc.SetKeyRepo(keyRepo)
		ukeySvc.SetLoginLogRepo(loginLogRepo)
		authSvc.SetUKeyService(ukeySvc)
		// 管理节点也让自己在集群里可见：集群界面上的"管理节点 N 个"
		// 需要有依据，否则运维看不出管理面是单点还是多活
		go runManagerHeartbeat(ctx, nodeSvc, cfg)
		slog.Info("短信验证码服务已就绪",
			"provider", smsProvider.Name(), "simulated", smsProvider.Simulated(),
			"env", os.Getenv("BASTION_ENV"))
	}

	if sessionSvc != nil && nodeSvc != nil {
		sessionKiller = &service.SessionKiller{
			Sessions: sessionSvc,
			Nodes:    nodeSvc,
			Secret:   cfg.Internal.Secret,
		}
	}

	// ---------- HTTP 服务 ----------
	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: api.NewRouter(api.Deps{
			Config:      cfg,
			Pool:        pool,
			Crypto:      prov,
			RoleRepo:    roleRepo,
			KeyRepo:     keyRepo,
			OplogRepo:   oplogRepo,
			Setup:       setupSvc,
			Auth:        authSvc,
			Asset:       assetSvc,
			User:        userSvc,
			Policy:      policySvc,
			Ticket:      ticketSvc,
			Ops:         opsSvc,
			Sms:         smsSvc,
			Captcha:     captchaStore,
			Selftest:    selftestSvc,
			Network:     networkSvc,
			Node:        nodeSvc,
			Cluster:     clusterSvc,
			Grant:       grantSvc,
			UKey:        ukeySvc,
			Session:     sessionSvc,
			SessionRepo: sessionRepo,
			SessionKiller: sessionKiller,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("管理服务启动",
			"addr", cfg.HTTPAddr,
			"version", config.Version,
			"crypto_provider", cfg.Crypto.Provider,
			"crypto_backend", cfg.Crypto.Backend,
			"db_connected", pool != nil)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "err", err)
			os.Exit(1)
		}
	}()

	// ---------- 开机自检与周期自检 ----------
	//
	// 开机自检**延迟几秒再跑**：密码服务与数据库此时刚装配完，
	// 立刻执行可能拿到"依赖未就绪"的假失败，反而制造误报。
	if selftestSvc != nil {
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			if _, err := selftestSvc.Run(ctx, repo.SelftestBoot, nil); err != nil {
				slog.Error("开机自检执行失败", "err", err)
			}
		}()
		selftestSvc.StartScheduler(ctx)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("收到停止信号，开始优雅关闭 ...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("优雅关闭失败", "err", err)
	}
	slog.Info("管理服务已停止")
}

// connectDB 连接数据库。
//
// 数据库不可用时的行为由 BASTION_DB_REQUIRED 决定：
//   - true（默认）：启动失败，避免带着不可用的依赖对外提供服务；
//   - false：仅告警并降级运行（便于前端联调等场景）。
func connectDB(ctx context.Context, cfg *config.Config) *pgxpool.Pool {
	pool, err := store.Open(ctx, cfg.DB.DSN(), cfg.DB.MaxConns)
	if err != nil {
		if cfg.DB.Required {
			slog.Error("数据库连接失败", "err", err,
				"host", cfg.DB.Host, "port", cfg.DB.Port, "db", cfg.DB.DBName)
			os.Exit(1)
		}
		slog.Warn("数据库不可用，以降级模式启动（部分接口不可用）", "err", err)
		return nil
	}
	slog.Info("数据库已连接", "host", cfg.DB.Host, "port", cfg.DB.Port, "db", cfg.DB.DBName)
	return pool
}

// autoMigrate 启动时自动应用未执行的迁移。
func autoMigrate(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	res, err := store.Migrate(ctx, pool, store.MigrateOptions{
		Dir:    cfg.DB.MigrationsDir,
		Logger: slog.Default(),
	})
	if err != nil {
		return err
	}
	if len(res.Applied) > 0 {
		slog.Info("数据库迁移完成", "applied", len(res.Applied), "source", res.Source)
	}
	return nil
}

// 确保 crypto 包被引用（Deps 使用该接口类型）。
var _ crypto.Provider
