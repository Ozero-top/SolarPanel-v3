package handler

import (
	"net/http"
	"strconv"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
)

// AuditHandler 审计日志接口
type AuditHandler struct{}

func NewAuditHandler() *AuditHandler { return &AuditHandler{} }

// Dispatch 路由分发
func (h *AuditHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := GetQuery(r, "action", "")
	if action == "" {
		action = r.FormValue("action")
	}
	switch action {
	case "list", "":
		h.List(w, r)
	case "clear":
		h.Clear(w, r)
	default:
		Err(w, http.StatusBadRequest, 1, "unknown action: "+action)
	}
}

// AuditLogRow 审计日志行
type AuditLogRow struct {
	ID          int    `json:"id"`
	Action      string `json:"action"`
	Target      string `json:"target"`
	Result      string `json:"result"`
	Actor       string `json:"actor"`
	ActorRole   string `json:"actor_role"`
	IP          string `json:"ip"`
	UserAgent   string `json:"user_agent"`
	Detail      string `json:"detail"`
	CreatedAt   string `json:"created_at"`
}

// List 分页查询审计日志（仅 admin 可见）
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 403, "仅管理员可查看审计日志")
		return
	}

	page, _ := strconv.Atoi(GetQuery(r, "page", "1"))
	pageSize, _ := strconv.Atoi(GetQuery(r, "pagesize", "50"))
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	if page < 1 {
		page = 1
	}

	var total int
	db.DB.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := db.DB.Query("SELECT id, action, target, result, actor, actor_role, ip, user_agent, detail, created_at FROM audit_logs ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?", pageSize, offset)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "查询失败")
		return
	}
	defer rows.Close()

	var logs []AuditLogRow
	for rows.Next() {
		var l AuditLogRow
		if err := rows.Scan(&l.ID, &l.Action, &l.Target, &l.Result, &l.Actor, &l.ActorRole, &l.IP, &l.UserAgent, &l.Detail, &l.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []AuditLogRow{}
	}

	OK(w, map[string]any{
		"total":    total,
		"page":     page,
		"pagesize": pageSize,
		"list":     logs,
	})
}

// Clear 清空审计日志
func (h *AuditHandler) Clear(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 403, "仅管理员可清空")
		return
	}

	db.DB.Exec("DELETE FROM audit_logs")
	OK(w, nil)
}
