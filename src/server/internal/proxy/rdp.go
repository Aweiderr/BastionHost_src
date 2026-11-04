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

// RDPGateway 是给 mstsc / FreeRDP 直连的 TCP 口，目前仍只回提示。
//
// 真正的画面在代理 HTTP 的 WebSocket `/api/v1/webdesktop`：
// 浏览器和桌面客户端走那条通道，由堡垒机完成授权、建 ops_session，
// 再拨到资产上的 3389。这里不解析 RDP，避免未授权的 TCP 透传。
// Enable=false（默认）时不监听。
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
		slog.Info("RDP 原生 TCP 口未启用（画面请走 /api/v1/webdesktop）")
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
	slog.Info("RDP 原生 TCP 口已监听（不提供画面，仅回提示）", "addr", g.Listen)

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

	msg := []byte("堡垒机不在这个 TCP 端口提供 RDP 画面。请使用 Web「图形会话」或桌面客户端（/api/v1/webdesktop）。\r\n")
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
