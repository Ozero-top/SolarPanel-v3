// Package urlutil 提供剪藏查重所需的 URL 归一化。
// 只做安全、可预测的等价化：协议/域名小写、去默认端口、去片段、去跟踪参数、查询参数排序。
package urlutil

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// trackingParams 常见的营销/跟踪参数，归一化时直接丢弃
var trackingParams = map[string]bool{
	"gclid": true, "fbclid": true, "msclkid": true, "yclid": true, "igshid": true,
}

type queryPair struct {
	key   string // 原始 key（保留编码形态）
	value string // 原始 value（保留编码形态）
	plain string // 解码后的 key，用于过滤与排序
	hasEq bool
}

// Normalize 返回用于查重的规范化 URL。
// 展示与打开仍应使用用户提交的原始 URL。
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("URL 不能为空")
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("URL 必须以 http:// 或 https:// 开头")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URL 解析失败")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("仅支持 http / https 协议")
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL 缺少域名")
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.User = nil // 不把凭据写进查重键
	u.Fragment = ""
	u.RawFragment = ""

	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	// IPv6 字面量需要保留方括号
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	if u.Path == "" {
		u.Path = "/"
	}

	u.RawQuery = normalizeQuery(u.RawQuery)
	return u.String(), nil
}

// normalizeQuery 去掉跟踪参数，其余按键稳定排序（同键保持原顺序）
func normalizeQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	parts := strings.Split(rawQuery, "&")
	pairs := make([]queryPair, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		k, v := part, ""
		hasEq := false
		if i := strings.IndexByte(part, '='); i >= 0 {
			k, v = part[:i], part[i+1:]
			hasEq = true
		}
		plain, err := url.QueryUnescape(k)
		if err != nil {
			plain = k
		}
		name := strings.ToLower(plain)
		if strings.HasPrefix(name, "utm_") || trackingParams[name] {
			continue
		}
		pairs = append(pairs, queryPair{key: k, value: v, plain: name, hasEq: hasEq})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].plain < pairs[j].plain })

	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.key)
		if p.hasEq || p.value != "" {
			b.WriteByte('=')
			b.WriteString(p.value)
		}
	}
	return b.String()
}

// Key 返回查重键；归一化失败时退回原始 URL（去掉首尾空白），保证不阻断写入。
func Key(raw string) string {
	if s, err := Normalize(raw); err == nil {
		return s
	}
	return strings.TrimSpace(raw)
}

// SameOrigin 判断两个 URL 是否同协议同主机，用于 canonical 采纳判断
func SameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}
