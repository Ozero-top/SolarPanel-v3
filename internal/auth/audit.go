package auth

import (
	"log"
	"net/http"
	"strings"

	"solarpanel/internal/db"
)

// AuditLog 写一条审计日志（失败静默）
func AuditLog(w http.ResponseWriter, r *http.Request, action, target, result, actor, actorRole, detail string) {
	if db.DB == nil {
		return
	}
	ip := ClientIP(r)
	ua := r.UserAgent()
	_, err := db.DB.Exec(`INSERT INTO audit_logs (action, target, result, actor, actor_role, ip, user_agent, detail) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		action, target, result, actor, actorRole, ip, ua, detail)
	if err != nil {
		log.Printf("[audit] write failed: %v", err)
	}
}

// ClientIP 解析真实客户端 IP（支持 X-Forwarded-For / X-Real-IP）
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

// AuditCleanup 清理 7 天前的审计日志（每小时调用一次）
func AuditCleanup() {
	if db.DB == nil {
		return
	}
	res, err := db.DB.Exec(`DELETE FROM audit_logs WHERE created_at < datetime('now','localtime','-7 days')`)
	if err != nil {
		log.Printf("[audit] cleanup failed: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("[audit] cleaned %d logs older than 7 days", n)
	}
}
