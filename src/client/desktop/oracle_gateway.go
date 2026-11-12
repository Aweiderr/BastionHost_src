package main

import (
	"net"
	"net/url"
	"strings"
)

// 与前端 oracleGateway.ts 的回退地址一致。回环和管理服务本机地址不能拿去填 SQL 客户端。
const oracleGatewayFallback = "192.168.106.114"

// oracleListenHost 选出本机 sqlplus / DBeaver 要连的堡垒机主机。端口固定 1521，不在这里返回。
//
// 调度给出了非回环的代理地址时用它。否则用管理服务主机名。两者都不可用时用前端同一常量。
// 资产台账上的 host 不会作为候选。
func oracleListenHost(direct bool, sshAddr, coreURL string) string {
	if !direct {
		if host := hostOfAddr(sshAddr); oracleHostUsable(host) {
			return host
		}
	}
	if host := hostOfCore(coreURL); oracleHostUsable(host) {
		return host
	}
	return oracleGatewayFallback
}

func hostOfAddr(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(raw, "[]")
}

func hostOfCore(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.Trim(u.Hostname(), "[]")
}

func oracleHostUsable(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "wails.localhost", "0.0.0.0":
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return false
	}
	return true
}
