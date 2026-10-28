package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
	"solarpanel/internal/urlutil"
)

// ClipHandler 统一剪藏接口：扩展、快捷指令、PWA 分享、网页端共用
type ClipHandler struct {
	cfg *config.Config

	mu       sync.Mutex
	winStart map[int]time.Time
	winCount map[int]int
}

func NewClipHandler(cfg *config.Config) *ClipHandler {
	return &ClipHandler{cfg: cfg, winStart: map[int]time.Time{}, winCount: map[int]int{}}
}

// clipPrincipal 剪藏请求的调用方身份
type clipPrincipal struct {
	UID      uint
	Username string
	Name     string
	Role     string
	TokenID  int
	Scopes   map[string]bool
	ViaToken bool
}

type clipInput struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Note      string `json:"note"`
	GroupID   int    `json:"group_id"`
	GroupName string `json:"group_name"`
	Capture   string `json:"capture"`
	Selection string `json:"selection"`
	Force     bool   `json:"force"`
	ClientID  string `json:"client_id"`
	Source    string `json:"-"`
}

// clipResult 返回给客户端的剪藏结果
type clipResult struct {
	Item      *ItemRow `json:"item,omitempty"`
	Group     string   `json:"group,omitempty"`
	Duplicate bool     `json:"duplicate"`
	Warnings  []string `json:"warnings"`
	Snapshot  bool     `json:"snapshot"`
}

var reBareURL = regexp.MustCompile(`https?://[^\s<>"']+`)

// extractURLFromText 从 Android 分享的 text/title 中兜底提取 URL
// （系统分享面板经常不填 url 字段，URL 会混在文本里）
func extractURLFromText(s string) string {
	m := reBareURL.FindString(s)
	if m == "" {
		return ""
	}
	return strings.TrimRight(m, ".,;:!?)]}，。；：！？）】")
}

func clipEnabled() bool {
	return db.GetSetting("clip_enabled") != "0"
}

// authenticate 支持 Bearer Token 与登录会话两种身份。
// 会话写操作必须带 X-CSRF-Token；Bearer 不走 Cookie/CSRF。
func (h *ClipHandler) authenticate(w http.ResponseWriter, r *http.Request) (*clipPrincipal, bool) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Authorization"))), "bearer ") {
		p, err := auth.AuthenticateBearer(r)
		if err != nil {
			Err(w, http.StatusUnauthorized, 1, "Token 无效或已过期")
			return nil, false
		}
		if !p.HasScope("clip:write") {
			Err(w, http.StatusForbidden, 1, "Token 缺少 clip:write 权限")
			return nil, false
		}
		return &clipPrincipal{UID: p.UID, Username: p.Username, Name: p.Name, Role: p.Role, TokenID: p.TokenID, Scopes: p.Scopes, ViaToken: true}, true
	}

	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "请先登录")
		return nil, false
	}
	if sess.Role != "admin" && sess.Role != "editor" {
		Err(w, http.StatusForbidden, 1, "无剪藏权限")
		return nil, false
	}
	if r.Method != http.MethodGet && !auth.VerifyCSRF(r) {
		Err(w, http.StatusForbidden, 1, "CSRF 校验失败")
		return nil, false
	}
	p := &clipPrincipal{
		UID: sess.UID, Role: sess.Role, TokenID: 0, ViaToken: false,
		Scopes: map[string]bool{"clip:write": true, "clip:read": true, "groups:read": true},
	}
	var username, name string
	db.DB.QueryRow("SELECT username, name FROM users WHERE id = ?", sess.UID).Scan(&username, &name)
	p.Username, p.Name = username, name
	return p, true
}

// allowToken 令牌维度限流：60 次/分钟（IP 维度由路由中间件承担）
func (h *ClipHandler) allowToken(tokenID int) bool {
	if tokenID == 0 {
		return true
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	start, ok := h.winStart[tokenID]
	if !ok || now.Sub(start) >= time.Minute {
		h.winStart[tokenID] = now
		h.winCount[tokenID] = 1
		return true
	}
	if h.winCount[tokenID] >= 60 {
		return false
	}
	h.winCount[tokenID]++
	return true
}

// Create POST /api/v1/clips
func (h *ClipHandler) Create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !clipEnabled() {
		Err(w, http.StatusForbidden, 1, "站点已关闭剪藏功能")
		return
	}
	if !h.allowToken(p.TokenID) {
		Err(w, http.StatusTooManyRequests, 1, "请求过于频繁，请稍后重试")
		return
	}
	var in clipInput
	if err := BindJSON(r, &in); err != nil {
		Fail(w, "参数错误")
		return
	}
	in.Source = "api"
	res, httpCode, msg := h.createClip(w, r, p, in)
	if httpCode != 0 {
		Err(w, httpCode, 1, msg)
		return
	}
	writeResp(w, http.StatusOK, res, msg)
}

func (h *ClipHandler) createClip(w http.ResponseWriter, r *http.Request, p *clipPrincipal, in clipInput) (*clipResult, int, string) {
	raw := strings.TrimSpace(in.URL)
	norm, err := urlutil.Normalize(raw)
	if err != nil {
		return nil, http.StatusBadRequest, err.Error()
	}
	key := urlutil.Key(norm)

	// 幂等：同一 Token 的同一 client_id 在 24 小时内只建一张卡
	if p.ViaToken && in.ClientID != "" {
		if it, ok := findClipRequest(p.TokenID, in.ClientID); ok {
			group := ""
			db.DB.QueryRow("SELECT title FROM item_groups WHERE id = ?", it.GroupID).Scan(&group)
			hasSnapshot := false
			if c, err := loadClipContent(it.ID); err == nil && c.ContentMD != "" {
				hasSnapshot = true
			}
			return &clipResult{Item: it, Group: group, Warnings: []string{}, Snapshot: hasSnapshot}, 0, "已保存"
		}
	}

	mode := clipCaptureMode(in.Capture)

	// 先按原始 URL 查重，force=true 跳过
	var existing *ItemRow
	if !in.Force {
		existing, _ = findItemByURLKey(key)
		if existing != nil {
			if existing.Title == "" && in.Title != "" {
				db.DB.Exec("UPDATE items SET title = ? WHERE id = ?", trimRunes(in.Title, 200), existing.ID)
				existing.Title = trimRunes(in.Title, 200)
			}
			group := ""
			db.DB.QueryRow("SELECT title FROM item_groups WHERE id = ?", existing.GroupID).Scan(&group)
			// 已存在但本次要求正文且此前没有快照时，顺手补齐快照
			snap, snapWarn := false, []string{}
			if current, err := loadClipContent(existing.ID); err == nil && current.ContentMD != "" {
				snap = true
			} else if mode == "snapshot" {
				page := fetchClipPage(norm)
				if page.FetchError != "" {
					snapWarn = append(snapWarn, "fetch_failed")
				} else if _, reason := saveClipSnapshot(existing.ID, key, raw, in.Source, existing.Title, "", page); reason != "" {
					snapWarn = append(snapWarn, reason)
				} else {
					snap = true
				}
			}
			auditClip(w, r, p, in.Source, "duplicate", existing.ID)
			return &clipResult{Item: existing, Group: group, Duplicate: true, Warnings: snapWarn, Snapshot: snap}, 0, "该链接已存在"
		}
	}

	// 服务端抓取（失败不阻断建卡）
	page := fetchClipPage(norm)
	warnings := []string{}
	if page.FetchError != "" {
		warnings = append(warnings, "fetch_failed")
	}
	if !in.Force && page.Canonical != "" {
		canonicalKey := urlutil.Key(page.Canonical)
		if canonicalKey != key {
			if found, _ := findItemByURLKey(canonicalKey); found != nil {
				group := ""
				db.DB.QueryRow("SELECT title FROM item_groups WHERE id = ?", found.GroupID).Scan(&group)
				auditClip(w, r, p, in.Source, "duplicate", found.ID)
				return &clipResult{Item: found, Group: group, Duplicate: true, Warnings: warnings, Snapshot: false}, 0, "该链接已存在"
			}
			key = canonicalKey
		}
	}

	host := ""
	if page.FinalURL != nil {
		host = strings.ToLower(page.FinalURL.Hostname())
	}
	if host == "" {
		if u, e := url.Parse(norm); e == nil {
			host = strings.ToLower(u.Hostname())
		}
	}

	title := trimRunes(strings.TrimSpace(in.Title), 200)
	if title == "" {
		title = trimRunes(page.Title, 200)
	}
	if title == "" {
		title = strings.TrimPrefix(host, "www.")
	}
	if title == "" {
		title = "未命名剪藏"
	}
	note := trimRunes(strings.TrimSpace(in.Note), 500)
	desc := note
	if desc == "" {
		desc = page.Desc
	}

	iconType, iconValue, coverPath := "favicon", "", ""
	if page.FetchError == "" {
		coverPath = saveClipCover(h.cfg, key, page.CoverURL)
		if coverPath != "" {
			iconType, iconValue = "image", coverPath
		} else if host != "" {
			iconValue = trySaveIconFromCandidates(h.cfg, host, clipIconCandidates(page, host))
		}
	}

	groupID, groupTitle, code, msg := resolveClipGroup(h.cfg, in.GroupID, in.GroupName)
	if code != 0 {
		return nil, code, msg
	}

	var nextSort int
	db.DB.QueryRow("SELECT COALESCE(MAX(sort), -1) + 1 FROM items WHERE group_id = ?", groupID).Scan(&nextSort)

	res, err := db.DB.Exec(`INSERT INTO items
		(group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at, url_key)
		VALUES (?,?,?,'',?,?,?,'',2,?,0,?,?,?)`,
		groupID, title, raw, desc, iconType, iconValue, nextSort, p.UID, db.NowStr(), key)
	if err != nil {
		return nil, http.StatusInternalServerError, "保存卡片失败"
	}
	newID, _ := res.LastInsertId()
	item, err := loadItem(int(newID))
	if err != nil {
		return nil, http.StatusInternalServerError, "保存卡片失败"
	}

	// 剪藏附加信息写入 description 之外不单独建表（P0 只维护卡片）
	if p.ViaToken && in.ClientID != "" {
		db.DB.Exec("INSERT OR REPLACE INTO clip_requests (token_id, client_id, item_id, created_at) VALUES (?,?,?,?)",
			p.TokenID, trimRunes(in.ClientID, 64), item.ID, db.NowStr())
	}

	// P1 正文快照：按保存方式抓正文、写 clip_contents 并同步全文索引
	snapshot := false
	if mode == "snapshot" {
		if page.FetchError != "" {
			warnings = appendUnique(warnings, "fetch_failed")
		} else if _, reason := saveClipSnapshot(item.ID, key, raw, in.Source, title, coverPath, page); reason != "" {
			warnings = appendUnique(warnings, reason)
		} else {
			snapshot = true
		}
	}
	auditClip(w, r, p, in.Source, "success", item.ID)
	return &clipResult{Item: item, Group: groupTitle, Warnings: warnings, Snapshot: snapshot}, 0, "已保存"
}

// appendUnique 追加不重复的告警码
func appendUnique(list []string, item string) []string {
	for _, v := range list {
		if v == item {
			return list
		}
	}
	return append(list, item)
}

// Delete DELETE /api/v1/clips：撤销刚保存的卡片（仅本人，管理员不限）
func (h *ClipHandler) Delete(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
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
	var owner int
	if err := db.DB.QueryRow("SELECT user_id FROM items WHERE id = ?", body.ID).Scan(&owner); err != nil {
		Fail(w, "卡片不存在")
		return
	}
	if p.Role != "admin" && owner != int(p.UID) {
		Err(w, http.StatusForbidden, 1, "只能撤销自己剪藏的卡片")
		return
	}
	dropClipSnapshot(body.ID)
	deleteItemTags(body.ID)
	if _, err := db.DB.Exec("DELETE FROM items WHERE id = ?", body.ID); err != nil {
		Fail(w, "删除失败")
		return
	}
	auditClip(w, r, p, "undo", "success", body.ID)
	OK(w, map[string]any{"id": body.ID})
}

// Preview POST /api/v1/preview：只抓元数据，不写库
func (h *ClipHandler) Preview(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !clipEnabled() {
		Err(w, http.StatusForbidden, 1, "站点已关闭剪藏功能")
		return
	}
	if !h.allowToken(p.TokenID) {
		Err(w, http.StatusTooManyRequests, 1, "请求过于频繁，请稍后重试")
		return
	}
	var in clipInput
	if err := BindJSON(r, &in); err != nil {
		Fail(w, "参数错误")
		return
	}
	norm, err := urlutil.Normalize(in.URL)
	if err != nil {
		Fail(w, err.Error())
		return
	}
	page := fetchClipPage(norm)
	key := urlutil.Key(norm)
	if page.Canonical != "" {
		key = urlutil.Key(page.Canonical)
	}
	existing, _ := findItemByURLKey(key)
	resp := map[string]any{
		"url":          norm,
		"url_key":      key,
		"title":        page.Title,
		"description":  page.Desc,
		"cover":        "",
		"duplicate":    existing != nil,
		"warnings":     []string{},
		"existing_id":  0,
	}
	if page.FetchError != "" {
		resp["warnings"] = []string{"fetch_failed"}
	}
	if existing != nil {
		resp["existing_id"] = existing.ID
		resp["title"] = existing.Title
		resp["description"] = existing.Description
		resp["cover"] = existing.IconValue
	} else if page.FetchError == "" && page.CoverURL != "" {
		// 预览不落盘，直接回源地址供前端预览（保存时再下载）
		resp["cover"] = page.CoverURL
	}
	OK(w, resp)
}

// Me GET /api/v1/me
func (h *ClipHandler) Me(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	scopes := make([]string, 0, 3)
	for _, s := range []string{"clip:write", "clip:read", "groups:read"} {
		if p.Scopes[s] || !p.ViaToken {
			scopes = append(scopes, s)
		}
	}
	OK(w, map[string]any{
		"user_id":      p.UID,
		"username":     p.Username,
		"name":         p.Name,
		"role":         p.Role,
		"scopes":       scopes,
		"clip_enabled": clipEnabled(),
		"token_id":     p.TokenID,
	})
}

// Groups GET /api/v1/groups
func (h *ClipHandler) Groups(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}
	rows, err := db.DB.Query(`SELECT id, parent_id, title FROM item_groups WHERE is_visible = 1 ORDER BY parent_id ASC, sort ASC, id ASC`)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取分组失败")
		return
	}
	defer rows.Close()
	type group struct {
		ID       int    `json:"id"`
		ParentID int    `json:"parent_id"`
		Title    string `json:"title"`
	}
	list := []group{}
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.ID, &g.ParentID, &g.Title); err == nil {
			list = append(list, g)
		}
	}
	OK(w, list)
}

// ===== 内部工具 =====

func loadItem(id int) (*ItemRow, error) {
	it, err := scanItem(db.DB.QueryRow("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	return it, nil
}

func findItemByURLKey(key string) (*ItemRow, error) {
	if key == "" {
		return nil, sql.ErrNoRows
	}
	it, err := scanItem(db.DB.QueryRow("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items WHERE url_key = ? ORDER BY id ASC LIMIT 1", key))
	if err != nil {
		return nil, err
	}
	return it, nil
}

func findClipRequest(tokenID int, clientID string) (*ItemRow, bool) {
	var itemID int
	err := db.DB.QueryRow("SELECT item_id FROM clip_requests WHERE token_id = ? AND client_id = ? AND created_at >= datetime('now','localtime','-1 day')",
		tokenID, trimRunes(clientID, 64)).Scan(&itemID)
	if err != nil {
		return nil, false
	}
	it, err := loadItem(itemID)
	if err != nil {
		return nil, false
	}
	return it, true
}

// resolveClipGroup 分组优先级：显式 id → 显式名称 → 站点默认分组 → 自动创建「稍后读」
func resolveClipGroup(cfg *config.Config, id int, name string) (int, string, int, string) {
	if id > 0 {
		var title string
		if err := db.DB.QueryRow("SELECT title FROM item_groups WHERE id = ? AND is_visible = 1", id).Scan(&title); err == nil {
			return id, title, 0, ""
		}
		return 0, "", http.StatusBadRequest, "分组不存在或未公开"
	}
	name = strings.TrimSpace(name)
	if name != "" {
		var gid int
		var title string
		if err := db.DB.QueryRow("SELECT id, title FROM item_groups WHERE title = ? AND is_visible = 1 ORDER BY parent_id ASC, id ASC LIMIT 1", name).Scan(&gid, &title); err == nil {
			return gid, title, 0, ""
		}
	}
	if v := strings.TrimSpace(db.GetSetting("clip_default_group")); v != "" && v != "0" {
		if gid, err := strconv.Atoi(v); err == nil && gid > 0 {
			var title string
			if err := db.DB.QueryRow("SELECT title FROM item_groups WHERE id = ? AND is_visible = 1", gid).Scan(&title); err == nil {
				return gid, title, 0, ""
			}
		}
	}

	const inbox = "稍后读"
	var gid int
	var title string
	if err := db.DB.QueryRow("SELECT id, title FROM item_groups WHERE title = ? AND parent_id = 0 ORDER BY id ASC LIMIT 1", inbox).Scan(&gid, &title); err == nil {
		return gid, title, 0, ""
	}
	res, err := db.DB.Exec("INSERT INTO item_groups (title, icon, description, parent_id, sort, is_visible) VALUES (?, '', ?, 0, 0, 1)",
		inbox, "剪藏收件箱")
	if err != nil {
		return 0, "", http.StatusInternalServerError, "创建默认分组失败"
	}
	newID, _ := res.LastInsertId()
	return int(newID), inbox, 0, ""
}

func auditClip(w http.ResponseWriter, r *http.Request, p *clipPrincipal, source, result string, itemID int) {
	actor := p.Username
	if actor == "" {
		actor = p.Name
	}
	detail := fmt.Sprintf("source=%s via=%s item=%d", source, map[bool]string{true: "token", false: "session"}[p.ViaToken], itemID)
	auth.AuditLog(w, r, "clip.create", fmt.Sprintf("item:%d", itemID), result, actor, p.Role, detail)
}

func trimRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n]))
}

// CleanupClipRequests 清理超过 24 小时的幂等记录，启动时与定时任务调用
func CleanupClipRequests() {
	if db.DB == nil {
		return
	}
	db.DB.Exec("DELETE FROM clip_requests WHERE created_at < datetime('now','localtime','-1 day')")
}
