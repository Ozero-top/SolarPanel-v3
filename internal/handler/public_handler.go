package handler

import (
	"encoding/base64"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

type PublicHandler struct {
	cfg *config.Config
}

func NewPublicHandler(cfg *config.Config) *PublicHandler {
	return &PublicHandler{cfg: cfg}
}

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
	"clip_ai_api_key":     true,
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
		log.Printf("[public] 读取设置失败: %v", err)
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
		log.Printf("[public] 读取分组失败: %v", err)
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
		log.Printf("[public] 读取卡片失败: %v", err)
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

	// 批量补齐标签，公开接口同步输出（前端暂不展示）
	ids := make([]int, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	tagMap := loadTagsForItems(ids)
	for i := range items {
		if tags, ok := tagMap[items[i].ID]; ok {
			items[i].Tags = tags
		}
	}

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

	// 首屏图标内联：第一个顶级分组（含其子分组）的前 10 张 favicon 卡片附带 data URL，
	// 只读本地缓存、不在请求内联网抓取；其余图标交给前端 IndexedDB 缓存 + 后台预取。
	h.inlineFirstScreenIcons(grouped)

	OK(w, map[string]any{
		"settings":       settings,
		"groups":         grouped,
		"user":           userInfo,
		"guest_required": guestRequired,
	})
}

// inlineIconLimit 首屏内联图标数量：与前端首屏直载预算一致
const inlineIconLimit = 10

// inlineFirstScreenIcons 给首个顶级分组链路（自身 + 子分组）的前几张 favicon 卡片填 IconData。
// 卡片按渲染顺序取：先本分组卡片，再子分组卡片。
func (h *PublicHandler) inlineFirstScreenIcons(groups []GroupWithItems) {
	if h.cfg == nil || len(groups) == 0 {
		return
	}
	byID := make(map[int]*GroupWithItems, len(groups))
	children := make(map[int][]int, len(groups))
	firstTop := -1
	for i := range groups {
		g := &groups[i]
		byID[g.ID] = g
		if g.ParentID == 0 {
			if firstTop < 0 {
				firstTop = g.ID
			}
		} else {
			children[g.ParentID] = append(children[g.ParentID], g.ID)
		}
	}
	if firstTop < 0 {
		return
	}

	count := 0
	var walk func(id int)
	walk = func(id int) {
		if count >= inlineIconLimit {
			return
		}
		g := byID[id]
		if g == nil {
			return
		}
		for i := range g.Items {
			if count >= inlineIconLimit {
				return
			}
			it := &g.Items[i]
			if it.IconType != "favicon" || it.URL == "" {
				continue
			}
			if data := cachedFaviconDataURL(h.cfg, it.URL); data != "" {
				it.IconData = data
				count++
			}
		}
		for _, cid := range children[id] {
			walk(cid)
		}
	}
	walk(firstTop)
}

// cachedFaviconDataURL 只读磁盘缓存，命中则转 data URL；未命中返回空（由前端走代理）。
func cachedFaviconDataURL(cfg *config.Config, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	disk, _, ext := faviconCachePath(cfg, host)
	if disk == "" {
		return ""
	}
	st, err := os.Stat(disk)
	if err != nil || st.Size() == 0 || st.Size() > 64*1024 {
		return ""
	}
	data, err := os.ReadFile(disk)
	if err != nil || len(data) == 0 {
		return ""
	}
	ct := faviconProxyContentTypes[ext]
	if ct == "" {
		return ""
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)
}
