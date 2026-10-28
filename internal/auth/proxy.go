package auth

import (
	"net"
	"net/http"
	"strings"
)

// IsHTTPS 判断当前请求是否为 HTTPS。
// 直连 TLS 直接为真；反向代理终止 TLS 时，仅当直连来源是内网/回环
// （典型 Docker + Nginx）才采信 X-Forwarded-Proto，避免公网客户端伪造。
func IsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isPrivateAddr(net.ParseIP(host)) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	return false
}
