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

// VNCGateway 是 VNC 画面通道的可运行骨架。
//
// 边界（明确未完成）：
//   - 不实现 RFB 握手与像素解码；
//   - 不接入授权/凭据/录像；
//   - 资产协议枚举已含 vnc（默认端口 5900），本骨架只打通监听与配置。
type VNCGateway struct {
	Listen  string
	Enable  bool
	Timeout time.Duration

	mu       sync.Mutex
	ln       net.Listener
	active   int
	shutdown bool
}

// ListenAndServe 启动 VNC 监听。
func (g *VNCGateway) ListenAndServe(ctx context.Context) error {
	if g == nil || !g.Enable {
		slog.Info("VNC 网关未启用（骨架就位，等待正式画面通道）")
		<-ctx.Done()
		return nil
	}
	if g.Listen == "" {
		g.Listen = ":5900"
	}
	ln, err := net.Listen("tcp", g.Listen)
	if err != nil {
		return fmt.Errorf("VNC 监听失败: %w", err)
	}
	g.mu.Lock()
	g.ln = ln
	g.mu.Unlock()
	slog.Info("VNC 网关骨架已启动（RFB 未实现）", "addr", g.Listen)

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
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			_, _ = c.Write([]byte("Bastion VNC gateway skeleton: RFB proxy not yet implemented.\n"))
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = io.Copy(io.Discard, io.LimitReader(c, 1024))
		}(conn)
	}
}
