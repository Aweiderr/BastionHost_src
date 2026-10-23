package api

import (
	"net"
	"net/http"
	"strings"

	"bastion.local/shared/httpx"
)

// parseCIDRs 解析 CIDR / 单 IP 列表；非法项跳过。
func parseCIDRs(list []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(list))
	for _, raw := range list {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			_, n, err := net.ParseCIDR(raw)
			if err == nil {
				out = append(out, n)
			}
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			out = append(out, &net.IPNet{IP: ip.To4(), Mask: net.CIDRMask(32, 32)})
		} else {
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
		}
	}
	return out
}

// ipAllowed 判断 IP 是否落在任一受信网段。
func ipAllowed(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	// 归一化 IPv4-mapped IPv6
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIPFromRequest 取直连对端 IP（不做 X-Forwarded-For）。
//
// /internal 接口的网络层隔离必须看真实 TCP 对端：若信任转发头，
// 外网攻击者只要伪造 X-Forwarded-For: 127.0.0.1 就能绕过。
func clientIPFromRequest(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return net.ParseIP(r.RemoteAddr)
	}
	return net.ParseIP(host)
}

// requireTrustedSource 限制处理器仅接受受信来源（loopback / 配置 CIDR）。
func requireTrustedSource(cidrs []string, next http.HandlerFunc) http.HandlerFunc {
	nets := parseCIDRs(cidrs)
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPFromRequest(r)
		if !ipAllowed(ip, nets) {
			httpx.FailCode(w, "API_4030")
			return
		}
		next(w, r)
	}
}
