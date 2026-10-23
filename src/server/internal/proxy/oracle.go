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

// OracleGateway 是 Oracle SQL 通道入口骨架。
//
// 边界（明确未完成）：
//   - 不解析 TNS/SQL*Net，不做语句审计与改写；
//   - 正式实现需：授权校验 → 凭据下发 → TNS 代理 → 语句截获写入 ops_session_command；
//   - 资产协议枚举已含 oracle（默认端口 1521），本文件接通监听与配置开关。
type OracleGateway struct {
	Listen string
	Enable bool

	mu       sync.Mutex
	ln       net.Listener
	shutdown bool
}

// ListenAndServe 启动 Oracle 代理入口。
func (g *OracleGateway) ListenAndServe(ctx context.Context) error {
	if g == nil || !g.Enable {
		slog.Info("Oracle 通道未启用（骨架就位，等待 SQL 代理实现）")
		<-ctx.Done()
		return nil
	}
	if g.Listen == "" {
		g.Listen = ":1521"
	}
	ln, err := net.Listen("tcp", g.Listen)
	if err != nil {
		return fmt.Errorf("Oracle 监听失败: %w", err)
	}
	g.mu.Lock()
	g.ln = ln
	g.mu.Unlock()
	slog.Info("Oracle SQL 通道骨架已启动（TNS 代理未实现）", "addr", g.Listen)

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
			_, _ = c.Write([]byte("Bastion Oracle gateway skeleton: TNS/SQL proxy not yet implemented.\n"))
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = io.Copy(io.Discard, io.LimitReader(c, 1024))
		}(conn)
	}
}
