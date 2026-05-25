package httpaccess

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// IPExtractor 按 trusted_proxies 白名单解析 X-Forwarded-For，返回最可信的客户端 IP。
type IPExtractor struct {
	trusted []*net.IPNet
}

// NewIPExtractor 校验并保存 CIDR 列表。空列表表示永远只用 RemoteAddr。
func NewIPExtractor(cidrs []string) (*IPExtractor, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for i, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("trusted_proxies[%d]=%q: %w", i, c, err)
		}
		nets = append(nets, n)
	}
	return &IPExtractor{trusted: nets}, nil
}

// ClientIP 返回请求的最可信来源 IP。
// 算法：若 RemoteAddr 不在 trusted 列表，直接返回；否则从 XFF 右到左找第一个 untrusted；
// 全部 trusted 退回最左项；XFF 空则用 RemoteAddr。
func (e *IPExtractor) ClientIP(r *http.Request) string {
	remote := hostOnly(r.RemoteAddr)
	if !e.isTrusted(remote) {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	parts := splitTrim(xff)
	for i := len(parts) - 1; i >= 0; i-- {
		if !e.isTrusted(parts[i]) {
			return parts[i]
		}
	}
	if len(parts) > 0 {
		return parts[0]
	}
	return remote
}

func (e *IPExtractor) isTrusted(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	for _, n := range e.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// hostOnly 剥掉 "host:port"，兼容 "[::1]:8080"、"1.2.3.4:8080" 和无端口形式。
func hostOnly(s string) string {
	h, _, err := net.SplitHostPort(s)
	if err != nil {
		return s
	}
	return h
}

func splitTrim(s string) []string {
	raw := strings.Split(s, ",")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HasUnrestrictedCIDR 报告 cidrs 中是否包含 IPv4/IPv6 全网（0.0.0.0/0 或 ::/0）。
// 用于配置启动期校验：access log 的 trusted_proxies 在非开发环境应禁止全开（否则
// XFF 头可被攻击者随意伪造）。
func HasUnrestrictedCIDR(cidrs []string) bool {
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		ones, bits := n.Mask.Size()
		if ones == 0 && bits != 0 {
			return true
		}
	}
	return false
}
