package handler

import (
	"net/http"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
)

type PublicHandler struct{}

// 访客访问日志去重：同一 IP 24 小时内只记一条，避免刷屏
var (
	guestVisitMu sync.Mutex
	guestVisited = map[string]time.Time{}
)

func shouldLogGuestVisit(ip string) bool {
	now := time.Now()
	guestVisitMu.Lock()
	defer guestVisitMu.Unlock()
	if t, ok := guestVisited[ip]; ok && now.Sub(t) < 24*time.Hour {
		return false
	}
	if len(guestVisited) > 2000 {
		for k, t := range guestVisited {
			if now.Sub(t) > 48*time.Hour {
				delete(guestVisited, k)
			}
		}
	}
	guestVisited[ip] = now
	return true
}

// maybeLogGuestVisit 记录访客（未登录 / guest 角色）的 IP 与归属地
func maybeLogGuestVisit(w http.ResponseWriter, r *http.Request) {
	if db.DB == nil {
		return
	}
	sess := auth.GetSessionFromRequest(r)
	if sess != nil && sess.Role != "guest" {
		return
	}
	ip := auth.ClientIP(r)
	if ip == "" || !shouldLogGuestVisit(ip) {
		return
	}
	// 进程重启后同一 IP 24h 内可能重复：先查库
	var n int
	db.DB.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='guest.visit' AND ip=? AND created_at>=datetime('now','localtime','-1 day')`, ip).Scan(&n)
	if n > 0 {
		return
	}
	auth.AuditLog(w, r, "guest.visit", "主页", "success", "访客", "guest", "访问主页")
}

// sensitiveSettingKeys 不允许下发给非管理员的设置项
var sensitiveSettingKeys = map[string]bool{
	"guest_password_hash": true,
}

type GroupWithItems struct {
	ItemGroupRow
	Items []ItemRow `json:"items"`
}

func (h *PublicHandler) Public(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	// 访客（未登录 / guest）访问记录：IP + 国家省市（同 IP 24h 去重）
	maybeLogGuestVisit(w, r)

	settingsQ, err := db.DB.Query("SELECT config_name, config_value FROM settings")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取设置失败")
		return
	}
	settings := make(map[string]string)
	for settingsQ.Next() {
		var name, val string
		settingsQ.Scan(&name, &val)
		if sensitiveSettingKeys[name] {
			continue
		}
		settings[name] = val
	}
	settingsQ.Close()

	sess := auth.GetSessionFromRequest(r)
	isAdmin := false
	canSeeHiddenItems := false
	var userInfo map[string]any = nil
	guestRequired := false

	if sess != nil {
		if sess.Role == "admin" || sess.Role == "editor" || sess.Role == "viewer" {
			isAdmin = true
		}
		if sess.Role == "admin" || sess.Role == "editor" {
			canSeeHiddenItems = true
		}
		if sess.Role != "guest" {
			var uName, uRole, uDBName string
			err := db.DB.QueryRow("SELECT username, name, role FROM users WHERE id=?", sess.UID).Scan(&uName, &uDBName, &uRole)
			if err == nil {
				userInfo = map[string]any{"username": uName, "name": uDBName, "role": uRole}
			}
		} else {
			userInfo = map[string]any{"role": "guest"}
		}
	} else {
		if settings["guest_access_enabled"] == "1" {
			guestRequired = true
		}
	}

	if guestRequired {
		OK(w, map[string]any{
			"settings":       settings,
			"groups":         []GroupWithItems{},
			"user":           nil,
			"guest_required": true,
		})
		return
	}

	groupQuery := "SELECT id, parent_id, title, icon, description, sort, is_visible, user_id, created_at FROM item_groups"
	if !isAdmin {
		groupQuery += " WHERE is_visible = 1"
	}
	groupQuery += " ORDER BY parent_id ASC, sort ASC, id ASC"
	groupQ, err := db.DB.Query(groupQuery)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取分组失败")
		return
	}
	var groups []ItemGroupRow
	for groupQ.Next() {
		var g ItemGroupRow
		groupQ.Scan(&g.ID, &g.ParentID, &g.Title, &g.Icon, &g.Description, &g.Sort, &g.IsVisible, &g.UserID, &g.CreatedAt)
		groups = append(groups, g)
	}
	groupQ.Close()

	itemQ, err := db.DB.Query("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items ORDER BY sort ASC, id ASC")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取卡片失败")
		return
	}
	var items []ItemRow
	for itemQ.Next() {
		var it ItemRow
		itemQ.Scan(&it.ID, &it.GroupID, &it.Title, &it.URL, &it.LanURL, &it.Description,
			&it.IconType, &it.IconValue, &it.IconBG, &it.OpenMethod, &it.Sort, &it.IsHidden,
			&it.UserID, &it.CreatedAt)
		items = append(items, it)
	}
	itemQ.Close()

	// 先按分组归档卡片，避免分组 × 卡片的双重遍历
	itemsByGroup := make(map[int][]ItemRow, len(groups))
	for _, it := range items {
		if !canSeeHiddenItems && it.IsHidden == 1 {
			continue
		}
		itemsByGroup[it.GroupID] = append(itemsByGroup[it.GroupID], it)
	}

	grouped := make([]GroupWithItems, 0, len(groups))
	for _, g := range groups {
		gw := GroupWithItems{ItemGroupRow: g, Items: itemsByGroup[g.ID]}
		if gw.Items == nil {
			gw.Items = []ItemRow{}
		}
		grouped = append(grouped, gw)
	}

	OK(w, map[string]any{
		"settings":       settings,
		"groups":         grouped,
		"user":           userInfo,
		"guest_required": guestRequired,
	})
}
