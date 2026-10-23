package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// RDPGateway 是 RDP 画面通道的可运行骨架。
//
// 边界（明确未完成）：
//   - 不解析 RDP 协议层（MCS/GCC/X.224），仅做 TCP 透明代理雏形；
//   - 不接入授权/凭据/录像；正式实现需在握手前完成身份与授权，
//     再按资产 host:port 建立到目标的 TLS/RDP 通道并切入录像。
//   - 默认监听由 BASTION_RDP_LISTEN 配置；Enable=false 时不监听。
type RDPGateway struct {
	Listen  string
	Enable  bool
	Timeout time.Duration

	mu       sync.Mutex
	ln       net.Listener
	active   int
	shutdown bool
}

// ListenAndServe 启动 RDP 监听，直到 ctx 结束。
func (g *RDPGateway) ListenAndServe(ctx context.Context) error {
	if g == nil || !g.Enable {
		slog.Info("RDP 网关未启用（骨架就位，等待正式画面通道）")
		<-ctx.Done()
		return nil
	}
	if g.Listen == "" {
		g.Listen = ":3389"
	}
	if g.Timeout <= 0 {
		g.Timeout = 10 * time.Second
	}
	ln, err := net.Listen("tcp", g.Listen)
	if err != nil {
		return fmt.Errorf("RDP 监听失败: %w", err)
	}
	g.mu.Lock()
	g.ln = ln
	g.mu.Unlock()
	slog.Info("RDP 网关骨架已启动（透明代理占位，未完成协议解析）", "addr", g.Listen)

	go func() {
		<-ctx.Done()
		g.mu.Lock()
		g.shutdown = true
		g.mu.Unlock()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			g.mu.Lock()
			down := g.shutdown
			g.mu.Unlock()
			if down || ctx.Err() != nil {
				return nil
			}
			slog.Warn("RDP Accept 失败", "err", err)
			continue
		}
		go g.handle(conn)
	}
}

func (g *RDPGateway) handle(c net.Conn) {
	defer c.Close()
	g.mu.Lock()
	g.active++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.active--
		g.mu.Unlock()
	}()

	msg := []byte("Bastion RDP gateway skeleton: protocol proxy not yet implemented. Configure asset protocol=rdp; full frame channel is pending.\r\n")
	_, _ = c.Write(msg)
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.Copy(io.Discard, io.LimitReader(c, 1024))
}

// Active 返回当前连接数。
func (g *RDPGateway) Active() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}
