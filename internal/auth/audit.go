package auth

import (
	"log"
	"net"
	"net/http"
	"strings"

	"solarpanel/internal/db"
)

// AuditLog 写一条审计日志（失败静默）；IP 归属地异步补齐，不阻塞请求
func AuditLog(w http.ResponseWriter, r *http.Request, action, target, result, actor, actorRole, detail string) {
	if db.DB == nil {
		return
	}
	ip := ClientIP(r)
	ua := r.UserAgent()
	res, err := db.DB.Exec(`INSERT INTO audit_logs (action, target, result, actor, actor_role, ip, user_agent, detail, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now','localtime'))`,
		action, target, result, actor, actorRole, ip, ua, detail)
	if err != nil {
		log.Printf("[audit] write failed: %v", err)
		return
	}
	if id, e := res.LastInsertId(); e == nil && ip != "" {
		go attachAuditRegion(id, ip)
	}
}

// attachAuditRegion 异步查询 IP 归属地并回填到审计记录（国家 + 省 + 市）
func attachAuditRegion(id int64, ip string) {
	if db.DB == nil {
		return
	}
	region := RegionForIP(ip)
	if region == "" {
		return
	}
	if _, err := db.DB.Exec("UPDATE audit_logs SET region=? WHERE id=?", region, id); err != nil {
		log.Printf("[audit] region update failed: %v", err)
	}
}

// isPrivateAddr 判断连接来源是否为内网/回环地址（即前置反向代理）
func isPrivateAddr(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	return false
}

// ClientIP 解析真实客户端 IP。
// 仅当直连来源是内网/回环（典型 Docker + Nginx 反代）时才采信 X-Forwarded-For / X-Real-IP，
// 直连公网客户端无法通过伪造转发头绕过限流或污染审计日志。
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(host)

	if isPrivateAddr(remoteIP) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[0]); ip != "" {
				return ip
			}
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}
	return host
}

// AuditCleanup 清理 30 天前的审计日志（每小时调用一次）
func AuditCleanup() {
	if db.DB == nil {
		return
	}
	res, err := db.DB.Exec(`DELETE FROM audit_logs WHERE created_at < datetime('now','localtime','-30 days')`)
	if err != nil {
		log.Printf("[audit] cleanup failed: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("[audit] cleaned %d logs older than 30 days", n)
	}
}
