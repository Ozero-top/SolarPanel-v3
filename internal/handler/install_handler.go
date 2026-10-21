package handler

import (
	"net/http"

	"solarpanel/internal/db"
)

// InstallHandler 检查数据库是否已初始化
type InstallHandler struct{}

// Status 返回安装状态：installed=true 表示已有管理员账号
func (InstallHandler) Status(w http.ResponseWriter, r *http.Request) {
	var count int
	db.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	OK(w, map[string]any{
		"installed":   count > 0,
		"app_version": AppVersion,
		"db_engine":   "SQLite",
		"php_rewrite": "1",
	})
}

// Setup 手动触发安装（仅用于手动重置后）
func (InstallHandler) Setup(w http.ResponseWriter, r *http.Request) {
	var count int
	db.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count > 0 {
		Err(w, http.StatusConflict, 1, "已安装，如要重置请使用 backup.php?action=reset")
		return
	}
	OK(w, map[string]any{"ok": true, "msg": "安装已触发，默认管理员 admin / admin123"})
}
