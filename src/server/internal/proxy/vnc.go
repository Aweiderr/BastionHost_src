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

// VNCGateway 是给本机 VNC 查看器直连的 TCP 口，目前仍只回提示。
//
// 画面走 `/api/v1/webdesktop`：代理完成授权和 RFB 认证后再把像素交给浏览器。
// 这个监听不做未授权的 RFB 透传。Enable=false（默认）时不监听。
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
		slog.Info("VNC 原生 TCP 口未启用（画面请走 /api/v1/webdesktop）")
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
	slog.Info("VNC 原生 TCP 口已监听（不提供画面，仅回提示）", "addr", g.Listen)

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
			_, _ = c.Write([]byte("堡垒机不在这个 TCP 端口提供 VNC 画面。请使用 Web「图形会话」或桌面客户端（/api/v1/webdesktop）。\n"))
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = io.Copy(io.Discard, io.LimitReader(c, 1024))
		}(conn)
	}
}
