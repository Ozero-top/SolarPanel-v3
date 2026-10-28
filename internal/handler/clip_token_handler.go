package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
)

// ClipTokenHandler 个人访问令牌管理（仅登录会话，不走 Bearer）
type ClipTokenHandler struct{}

type apiTokenView struct {
	ID         int    `json:"id"`
	UserID     int    `json:"user_id"`
	Owner      string `json:"owner"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Scopes     string `json:"scopes"`
	ExpiresAt  string `json:"expires_at"`
	LastUsedAt string `json:"last_used_at"`
	RevokedAt  string `json:"revoked_at"`
	CreatedAt  string `json:"created_at"`
}

// requireSession 校验登录、角色与 CSRF（写操作）
func requireClipTokenSession(w http.ResponseWriter, r *http.Request, needCSRF bool) (*auth.Session, bool) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "请先登录")
		return nil, false
	}
	if sess.Role != "admin" && sess.Role != "editor" {
		Err(w, http.StatusForbidden, 1, "无权限")
		return nil, false
	}
	if needCSRF && !auth.VerifyCSRF(r) {
		Err(w, http.StatusForbidden, 1, "CSRF 校验失败")
		return nil, false
	}
	return sess, true
}

// List GET /api/v1/tokens
func (h *ClipTokenHandler) List(w http.ResponseWriter, r *http.Request) {
	sess, ok := requireClipTokenSession(w, r, false)
	if !ok {
		return
	}
	q := `SELECT t.id, t.user_id, COALESCE(NULLIF(u.name,''), u.username), t.name, t.token_prefix,
			t.scopes, t.expires_at, t.last_used_at, t.revoked_at, t.created_at
		FROM api_tokens t LEFT JOIN users u ON u.id = t.user_id`
	args := []any{}
	if sess.Role != "admin" {
		q += " WHERE t.user_id = ?"
		args = append(args, sess.UID)
	}
	q += " ORDER BY t.id DESC"

	rows, err := db.DB.Query(q, args...)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取 Token 失败")
		return
	}
	defer rows.Close()
	list := []apiTokenView{}
	for rows.Next() {
		var t apiTokenView
		if err := rows.Scan(&t.ID, &t.UserID, &t.Owner, &t.Name, &t.Prefix, &t.Scopes,
			&t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt); err == nil {
			list = append(list, t)
		}
	}
	OK(w, list)
}

// Create POST /api/v1/tokens
func (h *ClipTokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	sess, ok := requireClipTokenSession(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name        string   `json:"name"`
		Scopes      []string `json:"scopes"`
		ExpiresDays int      `json:"expires_days"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	name := trimRunes(body.Name, 50)
	if name == "" {
		Fail(w, "请填写 Token 名称")
		return
	}

	allowed := map[string]bool{"clip:write": true, "clip:read": true, "groups:read": true}
	scopes := map[string]bool{"clip:write": true}
	for _, s := range body.Scopes {
		s = strings.TrimSpace(s)
		if allowed[s] {
			scopes[s] = true
		}
	}
	// 扩展与快捷指令至少需要 groups:read 才能选分组
	scopes["groups:read"] = true

	switch body.ExpiresDays {
	case 0, 30, 90, 365:
	default:
		Fail(w, "有效期仅支持 0/30/90/365 天")
		return
	}
	expiresAt := ""
	if body.ExpiresDays > 0 {
		expiresAt = time.Now().Add(time.Duration(body.ExpiresDays) * 24 * time.Hour).Format("2006-01-02 15:04:05")
	}

	plain, hash, prefix, err := auth.GenerateToken()
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "生成 Token 失败")
		return
	}
	res, err := db.DB.Exec(`INSERT INTO api_tokens (user_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked_at, created_at)
		VALUES (?,?,?,?,?,?,'','',?)`,
		sess.UID, name, hash, prefix, auth.JoinScopes(scopes), expiresAt, db.NowStr())
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "保存 Token 失败")
		return
	}
	id, _ := res.LastInsertId()
	auth.AuditLog(w, r, "clip.token.create", "token:"+trimRunes(name, 32), "success", "", sess.Role, "id="+strconv.Itoa(int(id)))

	OK(w, map[string]any{
		"id":         id,
		"name":       name,
		"token":      plain, // 仅本次返回，服务端只存哈希
		"prefix":     prefix,
		"scopes":     auth.JoinScopes(scopes),
		"expires_at": expiresAt,
	})
}

// Revoke DELETE /api/v1/tokens
func (h *ClipTokenHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	sess, ok := requireClipTokenSession(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID <= 0 {
		Fail(w, "参数错误")
		return
	}
	if sess.Role != "admin" {
		var owner int
		if err := db.DB.QueryRow("SELECT user_id FROM api_tokens WHERE id = ?", body.ID).Scan(&owner); err != nil || owner != int(sess.UID) {
			Err(w, http.StatusForbidden, 1, "只能吊销自己的 Token")
			return
		}
	}
	if _, err := db.DB.Exec("UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at = ''", db.NowStr(), body.ID); err != nil {
		Fail(w, "吊销失败")
		return
	}
	auth.AuditLog(w, r, "clip.token.revoke", "token:"+strconv.Itoa(body.ID), "success", "", sess.Role, "")
	OK(w, map[string]any{"id": body.ID})
}
