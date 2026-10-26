// Command server 是堡垒机服务端（协议代理）入口。
//
// 服务端承担数据面职责：协议代理、会话编排、命令管控、审计采集与录像封存
// （对应《堡垒机系统规划文档》服务端层）。
//
// 启动顺序（任一步失败都不进入"能连但不受控"的状态）：
//
//	1. 加载配置与密码服务（录像加密用本地密钥库）；
//	2. 建立到管理服务的服务间通道并做健康检查；
//	3. **强制拉取一次命令策略**：拉不到就不开启 SSH 监听 ——
//	   宁可运维入口暂不可用，也不能在没有策略的情况下放流量；
//	4. 启动 SSH 网关与观测用 HTTP 接口。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bastion.local/server/internal/api"
	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
	"bastion.local/server/internal/nodebeat"
	"bastion.local/server/internal/policycache"
	"bastion.local/server/internal/proxy"
	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/factory"
)

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
	startedAt := time.Now()

	// ---------- 1. 密码服务（工厂模式，录像加密用）----------
	var prov crypto.Provider
	if cfg.Proxy.SSHListen != "" {
		p, err := factory.New(factory.Config{
			Provider:      cfg.Crypto.Provider,
			Backend:       cfg.Crypto.Backend,
			KeyStoreDir:   cfg.Crypto.KeyStoreDir,
			MasterKeyFile: cfg.Crypto.MasterKeyFile,
			Pkcs11LibPath: cfg.Crypto.Pkcs11LibPath,
			Pkcs11Pin:     cfg.Crypto.Pkcs11Pin,
			Pkcs11Slot:    cfg.Crypto.Pkcs11Slot,
		})
		if err != nil {
			slog.Error("密码服务初始化失败", "err", err)
			os.Exit(1)
		}
		prov = p
		slog.Info("密码服务已就绪", "provider", prov.Name(), "backend", prov.Backend())
	}

	// ---------- 2. 管理面通道 ----------
	if cfg.Manager.Secret == "" {
		slog.Error("BASTION_INTERNAL_SECRET 未配置：无法与管理服务建立可信通道",
			"hint", "该密钥必须与管理服务的 BASTION_INTERNAL_SECRET 一致")
		os.Exit(1)
	}
	mc := managerclient.New(cfg.Manager.BaseURL, cfg.Manager.Secret, cfg.Node.ID, cfg.Manager.Timeout)

	hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
	err = mc.Healthz(hctx)
	hcancel()
	if err != nil {
		slog.Warn("管理服务暂不可达，将在策略拉取阶段重试", "url", cfg.Manager.BaseURL, "err", err)
	} else {
		slog.Info("管理服务已连接", "url", cfg.Manager.BaseURL, "node_id", cfg.Node.ID)
	}

	// ---------- 3. 命令策略（必须在开启 SSH 监听前加载成功）----------
	//
	// 带重试：管理服务与本服务通常被 systemd 同时拉起（本单元 Requires
	// bastion-manager），但"启动顺序正确"不等于"对端已能服务"——
	// 管理服务还要建库连池、初始化密钥，期间 8080 尚未监听。
	// 早期实现只试一次，于是出现"管理服务慢几秒 → SSH 网关永久不启动"，
	// 只能靠人工重启恢复。
	policies := policycache.New(mc, cfg.Manager.PolicyRefresh)
	policyReady := false
	for attempt := 1; attempt <= 10; attempt++ {
		pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
		err = policies.Refresh(pctx)
		pcancel()
		if err == nil {
			policyReady = true
			break
		}
		slog.Warn("命令策略加载失败，稍后重试", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	if policyReady {
		go policies.Run(ctx)
	} else {
		slog.Error("命令策略仍不可用，SSH 网关暂不开启（避免无策略放行）；"+
			"后台每 30 秒重试，就绪后自动开启", "err", err,
			"hint", "请确认管理服务可用且 BASTION_INTERNAL_SECRET 一致")
	}

	// ---------- 4. 录像加密密钥 ----------
	recordKeyID := ""
	if prov != nil {
		recordKeyID = ensureRecordKey(ctx, prov)
	}

	// ---------- 5. SSH 网关 ----------
	//
	// 网关实例总是创建（HTTP 观测接口需要它上报活跃会话数），
	// 但只有在策略就绪后才真正开始监听。
	var sshSrv *proxy.Server
	var beater *nodebeat.Beater
	if prov != nil {
		sshSrv = proxy.NewServer(cfg, mc, policies, prov, recordKeyID)

		// 心跳：向管理服务上报负载，并取回"是否允许接收新会话"。
		// 先于监听启动，让节点一上线就在集群界面可见。
		beater = nodebeat.New(mc, cfg, sshSrv.ActiveSessions)
		beater.SetLiveSessionIDs(sshSrv.ListLiveSessionIDs)
		beater.Start(ctx)
		// 维护状态（draining）下拒绝新会话，存量会话继续跑完
		sshSrv.SetAdmissionCheck(beater.Allowed)

		// RDP / VNC / Oracle 通道骨架（默认关闭，见 PROTOCOL_SKELETON.md）
		rdpGw := &proxy.RDPGateway{Listen: cfg.Proxy.RDPListen, Enable: cfg.Proxy.RDPEnable, Timeout: cfg.Proxy.ConnectTimeout}
		vncGw := &proxy.VNCGateway{Listen: cfg.Proxy.VNCListen, Enable: cfg.Proxy.VNCEnable, Timeout: cfg.Proxy.ConnectTimeout}
		oracleGw := &proxy.OracleGateway{Listen: cfg.Proxy.OracleListen, Enable: cfg.Proxy.OracleEnable}
		go func() { _ = rdpGw.ListenAndServe(ctx) }()
		go func() { _ = vncGw.ListenAndServe(ctx) }()
		go func() { _ = oracleGw.ListenAndServe(ctx) }()

		listen := func() {
			go func() {
				if err := sshSrv.ListenAndServe(ctx); err != nil {
					slog.Error("SSH 网关退出", "err", err)
					cancel()
				}
			}()
		}
		if policyReady {
			listen()
		} else {
			slog.Warn("SSH 网关待命：命令策略未就绪")
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-time.After(30 * time.Second):
					}
					if rerr := policies.Refresh(ctx); rerr == nil {
						slog.Info("命令策略已就绪，开启 SSH 网关", "rules", policies.Status()["rules"])
						go policies.Run(ctx)
						listen()
						return
					}
				}
			}()
		}
	}

	// ---------- 6. 观测用 HTTP 接口 ----------
	//
	// beater 为 nil（未启用密码服务）时不要把 *Beater 直接装进接口：
	// 空指针装进接口后接口本身不等于 nil，调用 Status() 会解引用空指针。
	var beatStatus api.NodeBeatStatus
	if beater != nil {
		beatStatus = beater
	}
	httpSrv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: api.NewRouter(api.Deps{
			Config: cfg, Manager: mc, Policies: policies, SSH: sshSrv, StartedAt: startedAt,
			NodeBeat: beatStatus,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("服务端 HTTP 已启动", "addr", cfg.HTTPAddr, "version", config.Version,
			"ssh_listen", cfg.Proxy.SSHListen, "ssh_enabled", sshSrv != nil)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "err", err)
			cancel()
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
		slog.Info("收到停止信号，开始优雅关闭 ...")
	case <-ctx.Done():
		slog.Info("上下文结束，开始优雅关闭 ...")
	}

	// 先收尾数据面会话，再停观测 HTTP。
	//
	// 顺序不能反：会话归档要经内部接口上报管理面，观测 HTTP 停掉并不
	// 影响它；而把"断开连接"放到最后，进程会在退出前一直阻塞在会话 IO 上，
	// 于是留下永久 active 的会话与没有封存尾的录像。
	sessionCtx, sessionCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if sshSrv != nil {
		sshSrv.Shutdown(sessionCtx)
	}
	sessionCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP 优雅关闭失败", "err", err)
	}
	slog.Info("服务端已停止")
}

// ensureRecordKey 确保存在录像加密密钥并返回其 ID。
//
// 密钥保存在代理节点本地密钥库：录像密钥与资产凭据密钥分离，
// 代理节点被攻破最多影响录像机密性，不会扩散资产凭据。
func ensureRecordKey(ctx context.Context, prov crypto.Provider) string {
	keys, err := prov.KeyList(ctx, crypto.KeyFilter{
		Usage: crypto.UsageDataEncrypt, Alias: "record_encrypt",
	})
	if err == nil && len(keys) > 0 {
		return keys[0].KeyID
	}
	meta, err := prov.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM4,
		Usage:   crypto.UsageDataEncrypt,
		Alias:   "record_encrypt",
		Remark:  "会话录像加密密钥（代理节点本地）",
	})
	if err != nil {
		slog.Error("录像加密密钥创建失败，录像将不可用", "err", err)
		return ""
	}
	slog.Info("已创建录像加密密钥", "key_id", meta.KeyID)
	return meta.KeyID
}
