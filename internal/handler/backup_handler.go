package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
	"solarpanel/internal/settings"
	"solarpanel/internal/urlutil"
)

type BackupHandler struct {
	cfg *config.Config
}

func NewBackupHandler(cfg *config.Config) *BackupHandler {
	return &BackupHandler{cfg: cfg}
}

type backupPayload struct {
	Version  string            `json:"version"`
	Time     string            `json:"time"`
	Settings []SettingRow      `json:"settings"`
	Groups   []ItemGroupRow    `json:"groups"`
	Items    []ItemRow         `json:"items"`
	Feeds    []CustomFeedRow   `json:"feeds"`
	Files    map[string]string `json:"files"`
	// 剪藏：old item id -> url_key，保证恢复后查重可用
	URLKeys      map[string]string   `json:"url_keys,omitempty"`
	Tokens       []backupTokenRow    `json:"tokens,omitempty"`
	ClipContents []backupClipContent `json:"clip_contents,omitempty"`
	ItemTags     []backupItemTag     `json:"item_tags,omitempty"`
}

// backupItemTag 卡片标签（P2）
type backupItemTag struct {
	ItemID int    `json:"item_id"`
	Tag    string `json:"tag"`
}

// backupTokenRow 个人访问令牌（只含哈希，不含明文）
type backupTokenRow struct {
	ID          int    `json:"id"`
	UserID      int    `json:"user_id"`
	Name        string `json:"name"`
	TokenHash   string `json:"token_hash"`
	TokenPrefix string `json:"token_prefix"`
	Scopes      string `json:"scopes"`
	ExpiresAt   string `json:"expires_at"`
	LastUsedAt  string `json:"last_used_at"`
	RevokedAt   string `json:"revoked_at"`
	CreatedAt   string `json:"created_at"`
}

// backupClipContent 正文快照（P0 无数据，保持口径一致）
type backupClipContent struct {
	ItemID      int    `json:"item_id"`
	URLKey      string `json:"url_key"`
	URLOriginal string `json:"url_original"`
	Source      string `json:"source"`
	Excerpt     string `json:"excerpt"`
	ContentMD   string `json:"content_md"`
	CoverPath   string `json:"cover_path"`
	FetchedAt   string `json:"fetched_at"`
	HTTPStatus  int    `json:"http_status"`
	ContentHash string `json:"content_hash"`
	WordCount   int    `json:"word_count"`
	FetchError  string `json:"fetch_error"`
}

const (
	backupMaxFileSize = 20 * 1024 * 1024
	backupKeepCount   = 5
)

// backupAllowedFileExt 备份只允许还原图片类资源，避免任意文件写入上传目录
var backupAllowedFileExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"webp": true, "bmp": true, "ico": true, "svg": true,
}

// backupFileMeta 服务端备份文件信息（返回给前端）
type backupFileMeta struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Created string `json:"created"`
}

type backupFileEntry struct {
	Name    string
	Size    int64
	ModTime time.Time
	Path    string
}

func (e backupFileEntry) meta() backupFileMeta {
	return backupFileMeta{
		Name:    e.Name,
		Size:    e.Size,
		Created: e.ModTime.Format("2006-01-02 15:04:05"),
	}
}

func (h *BackupHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")

	if r.Method == "GET" {
		switch action {
		case "", "export":
			h.Export(w, r)
		case "list":
			h.List(w, r)
		case "download":
			h.Download(w, r)
		default:
			Fail(w, "未知操作")
		}
		return
	}
	if r.Method == "POST" {
		switch action {
		case "import":
			h.Import(w, r)
		case "reset":
			h.Reset(w, r)
		case "create":
			h.Create(w, r)
		case "restore":
			h.Restore(w, r)
		case "delete":
			h.Delete(w, r)
		default:
			Fail(w, "未知操作")
		}
		return
	}
	Fail(w, "不支持的请求方法")
}

// requireAdmin 备份相关操作统一鉴权
func (h *BackupHandler) requireAdmin(w http.ResponseWriter, r *http.Request, msg string) bool {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, msg)
		return false
	}
	return true
}

func (h *BackupHandler) Export(w http.ResponseWriter, r *http.Request) {
	// 备份包含全部设置（含访客密码哈希）与上传资源，仅管理员可导出
	if !h.requireAdmin(w, r, "仅管理员可导出备份") {
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "html" {
		Fail(w, "不支持的导出格式")
		return
	}

	// HTML 书签不含图片资源，跳过 uploads 遍历，导出更快
	payload, err := h.collectPayload(format == "json")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, err.Error())
		return
	}

	now := time.Now()
	if format == "html" {
		data := buildBookmarksHTML(payload.Groups, payload.Items)
		filename := fmt.Sprintf("solarpanel-bookmarks-%s.html", now.Format("20060102-150405"))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "序列化失败")
		return
	}

	filename := fmt.Sprintf("solarpanel-backup-%s.json", now.Format("20060102-150405"))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

// collectPayload 读取备份所需的全部数据；includeFiles=false 时不含图片文件
func (h *BackupHandler) collectPayload(includeFiles bool) (backupPayload, error) {
	var payload backupPayload

	settingQ, err := db.DB.Query("SELECT id, config_name, config_value FROM settings")
	if err != nil {
		return payload, fmt.Errorf("读取设置失败")
	}
	for settingQ.Next() {
		var s SettingRow
		settingQ.Scan(&s.ID, &s.ConfigName, &s.ConfigValue)
		payload.Settings = append(payload.Settings, s)
	}
	settingQ.Close()

	groupQ, err := db.DB.Query("SELECT id, parent_id, title, icon, description, sort, is_visible, user_id, created_at FROM item_groups ORDER BY parent_id ASC, sort ASC, id ASC")
	if err != nil {
		return payload, fmt.Errorf("读取分组失败")
	}
	for groupQ.Next() {
		var g ItemGroupRow
		groupQ.Scan(&g.ID, &g.ParentID, &g.Title, &g.Icon, &g.Description, &g.Sort, &g.IsVisible, &g.UserID, &g.CreatedAt)
		payload.Groups = append(payload.Groups, g)
	}
	groupQ.Close()

	itemQ, err := db.DB.Query("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items ORDER BY sort ASC, id ASC")
	if err != nil {
		return payload, fmt.Errorf("读取卡片失败")
	}
	for itemQ.Next() {
		var it ItemRow
		itemQ.Scan(&it.ID, &it.GroupID, &it.Title, &it.URL, &it.LanURL, &it.Description, &it.IconType, &it.IconValue, &it.IconBG, &it.OpenMethod, &it.Sort, &it.IsHidden, &it.UserID, &it.CreatedAt)
		payload.Items = append(payload.Items, it)
	}
	itemQ.Close()

	feedQ, err := db.DB.Query("SELECT id, title, url, sort, enabled, created_at FROM custom_feeds ORDER BY sort ASC, id ASC")
	if err != nil {
		return payload, fmt.Errorf("读取 RSS 源失败")
	}
	for feedQ.Next() {
		var f CustomFeedRow
		feedQ.Scan(&f.ID, &f.Title, &f.URL, &f.Sort, &f.Enabled, &f.CreatedAt)
		payload.Feeds = append(payload.Feeds, f)
	}
	feedQ.Close()

	// 剪藏查重键（按旧 id 记录，恢复时配合新 id 映射写回）
	payload.URLKeys = map[string]string{}
	if keyQ, err := db.DB.Query("SELECT id, url_key FROM items"); err == nil {
		for keyQ.Next() {
			var id int
			var key string
			if keyQ.Scan(&id, &key) == nil && key != "" {
				payload.URLKeys[strconv.Itoa(id)] = key
			}
		}
		keyQ.Close()
	}

	if tokenQ, err := db.DB.Query(`SELECT id, user_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked_at, created_at
		FROM api_tokens ORDER BY id ASC`); err == nil {
		for tokenQ.Next() {
			var t backupTokenRow
			if tokenQ.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.TokenPrefix, &t.Scopes,
				&t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt) == nil {
				payload.Tokens = append(payload.Tokens, t)
			}
		}
		tokenQ.Close()
	}

	if contentQ, err := db.DB.Query(`SELECT item_id, url_key, url_original, source, excerpt, content_md, cover_path,
			fetched_at, http_status, content_hash, word_count, fetch_error FROM clip_contents ORDER BY item_id ASC`); err == nil {
		for contentQ.Next() {
			var c backupClipContent
			if contentQ.Scan(&c.ItemID, &c.URLKey, &c.URLOriginal, &c.Source, &c.Excerpt, &c.ContentMD, &c.CoverPath,
				&c.FetchedAt, &c.HTTPStatus, &c.ContentHash, &c.WordCount, &c.FetchError) == nil {
				payload.ClipContents = append(payload.ClipContents, c)
			}
		}
		contentQ.Close()
	}

	if tagQ, err := db.DB.Query(`SELECT item_id, tag FROM item_tags ORDER BY item_id ASC, tag ASC`); err == nil {
		for tagQ.Next() {
			var t backupItemTag
			if tagQ.Scan(&t.ItemID, &t.Tag) == nil {
				payload.ItemTags = append(payload.ItemTags, t)
			}
		}
		tagQ.Close()
	}

	payload.Version = "1.0"
	payload.Time = time.Now().Format("2006-01-02 15:04:05")

	if !includeFiles {
		return payload, nil
	}

	files := make(map[string]string)

	err = filepath.Walk(h.cfg.UploadDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(h.cfg.UploadDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.Size() > backupMaxFileSize {
			log.Printf("[backup] 跳过过大文件: %s (%d bytes)", rel, info.Size())
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[backup] 读取文件失败: %s: %v", rel, err)
			return nil
		}
		files[rel] = base64.StdEncoding.EncodeToString(data)
		return nil
	})
	if err != nil {
		log.Printf("[backup] 遍历上传目录失败: %v", err)
	}
	payload.Files = files
	return payload, nil
}

// buildBookmarksHTML 生成 Netscape 书签格式，可导入 Chrome / Edge 收藏夹。
// 仅包含可见分组与未隐藏卡片，不写入任何图标 / 壁纸 / favicon。
func buildBookmarksHTML(groups []ItemGroupRow, items []ItemRow) []byte {
	children := make(map[int][]ItemGroupRow)
	groupItems := make(map[int][]ItemRow)
	visibleIDs := make(map[int]bool)

	for _, g := range groups {
		if g.IsVisible != 1 {
			continue
		}
		visibleIDs[g.ID] = true
		children[g.ParentID] = append(children[g.ParentID], g)
	}
	for _, it := range items {
		if it.IsHidden != 0 || !visibleIDs[it.GroupID] {
			continue
		}
		groupItems[it.GroupID] = append(groupItems[it.GroupID], it)
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE NETSCAPE-Bookmark-file-1>\n")
	b.WriteString("<!-- This is an automatically generated file. -->\n")
	b.WriteString("<META HTTP-EQUIV=\"Content-Type\" CONTENT=\"text/html; charset=UTF-8\">\n")
	b.WriteString("<TITLE>Bookmarks</TITLE>\n")
	b.WriteString("<H1>Bookmarks</H1>\n")
	b.WriteString("<DL><p>\n")

	var writeGroup func(g ItemGroupRow, indent string)
	writeGroup = func(g ItemGroupRow, indent string) {
		fmt.Fprintf(&b, "%s<DT><H3>%s</H3>\n", indent, html.EscapeString(g.Title))
		fmt.Fprintf(&b, "%s<DL><p>\n", indent)
		for _, it := range groupItems[g.ID] {
			url := strings.TrimSpace(it.URL)
			if url == "" {
				url = strings.TrimSpace(it.LanURL)
			}
			if url == "" {
				continue
			}
			fmt.Fprintf(&b, "%s    <DT><A HREF=\"%s\">%s</A>\n", indent, html.EscapeString(url), html.EscapeString(it.Title))
		}
		for _, child := range children[g.ID] {
			writeGroup(child, indent+"    ")
		}
		fmt.Fprintf(&b, "%s</DL><p>\n", indent)
	}

	for _, g := range children[0] {
		writeGroup(g, "    ")
	}
	b.WriteString("</DL><p>\n")
	return []byte(b.String())
}

func (h *BackupHandler) Import(w http.ResponseWriter, r *http.Request) {
	// 导入会整体替换设置/分组/卡片并写入上传目录，仅管理员可执行
	if !h.requireAdmin(w, r, "仅管理员可导入备份") {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 128<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		file, _, err = r.FormFile("backup")
	}
	if err != nil {
		Fail(w, "未找到备份文件: "+err.Error())
		return
	}
	defer file.Close()

	body, err := io.ReadAll(file)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取文件失败")
		return
	}

	text := strings.TrimSpace(string(body))
	var payload backupPayload

	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
		if err := json.Unmarshal(decoded, &payload); err != nil {
			Fail(w, "备份文件内容不是有效的 JSON")
			return
		}
	} else {
		if err := json.Unmarshal(body, &payload); err != nil {
			Fail(w, "备份文件格式无效（base64 或纯 JSON）")
			return
		}
	}

	counts, err := h.applyPayload(payload)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "导入失败: "+err.Error())
		return
	}
	OK(w, counts)
}

// applyPayload 将备份数据整体写入数据库，并按需还原上传文件
func (h *BackupHandler) applyPayload(payload backupPayload) (map[string]int, error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("无法开启事务")
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM clip_contents")
	tx.Exec("DELETE FROM item_tags")
	tx.Exec("DELETE FROM clip_requests")
	tx.Exec("DELETE FROM api_tokens")
	tx.Exec("DELETE FROM items")
	tx.Exec("DELETE FROM item_groups")
	tx.Exec("DELETE FROM custom_feeds")
	tx.Exec("DELETE FROM settings")

	for _, s := range payload.Settings {
		tx.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", s.ConfigName, s.ConfigValue)
	}

	// 关键：按 parent_id ASC 排序，确保父分组先于子分组被插入，此时 groupIDMap 已有父分组映射
	sort.SliceStable(payload.Groups, func(i, j int) bool {
		if payload.Groups[i].ParentID != payload.Groups[j].ParentID {
			return payload.Groups[i].ParentID < payload.Groups[j].ParentID
		}
		return payload.Groups[i].ID < payload.Groups[j].ID
	})

	var groupIDMap = make(map[int64]int64)
	var itemIDMap = make(map[int64]int64)
	for _, g := range payload.Groups {
		// 重映射 parent_id：备份中的老父分组 id 重新插入后变成新 id，必须通过 groupIDMap 转换
		// 备份里 groups 按 parent_id ASC 排序，父分组先处理，此时 groupIDMap 一定已有父分组映射
		newParentID := g.ParentID
		if g.ParentID != 0 {
			if mapped, ok := groupIDMap[int64(g.ParentID)]; ok {
				newParentID = int(mapped)
			} else {
				log.Printf("[backup] 警告：子分组 '%s' (old_id=%d) 的父分组 old_id=%d 未找到映射，降级为顶级",
					g.Title, g.ID, g.ParentID)
				newParentID = 0
			}
		}
		res, err := tx.Exec("INSERT INTO item_groups (parent_id, title, icon, description, sort, is_visible, user_id) VALUES (?, ?, ?, ?, ?, ?, ?)",
			newParentID, g.Title, sanitizeGroupIcon(g.Icon), g.Description, g.Sort, g.IsVisible, 1)
		if err != nil {
			log.Printf("[backup] 导入分组 '%s' 失败: %v", g.Title, err)
			continue
		}
		newID, _ := res.LastInsertId()
		groupIDMap[int64(g.ID)] = newID
	}

	for _, it := range payload.Items {
		newGroupID, ok := groupIDMap[int64(it.GroupID)]
		if !ok {
			continue
		}
		// url_key 优先取备份记录，旧备份没有时按 URL 现算，保证恢复后查重可用
		key := payload.URLKeys[strconv.Itoa(it.ID)]
		if key == "" {
			key = urlutil.Key(it.URL)
		}
		res, err := tx.Exec("INSERT INTO items (group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, url_key) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)",
			newGroupID, it.Title, it.URL, it.LanURL, it.Description, it.IconType, it.IconValue, it.IconBG, it.OpenMethod, it.Sort, it.IsHidden, 1, key)
		if err == nil {
			if newID, e := res.LastInsertId(); e == nil {
				itemIDMap[int64(it.ID)] = newID
			}
		}
	}

	for _, t := range payload.Tokens {
		tx.Exec(`INSERT INTO api_tokens (user_id, name, token_hash, token_prefix, scopes, expires_at, last_used_at, revoked_at, created_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			t.UserID, t.Name, t.TokenHash, t.TokenPrefix, t.Scopes, t.ExpiresAt, t.LastUsedAt, t.RevokedAt, t.CreatedAt)
	}

	for _, c := range payload.ClipContents {
		newID, ok := itemIDMap[int64(c.ItemID)]
		if !ok {
			continue
		}
		tx.Exec(`INSERT OR REPLACE INTO clip_contents (item_id, url_key, url_original, source, excerpt, content_md, cover_path,
				fetched_at, http_status, content_hash, word_count, fetch_error) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID, c.URLKey, c.URLOriginal, c.Source, c.Excerpt, c.ContentMD, c.CoverPath,
			c.FetchedAt, c.HTTPStatus, c.ContentHash, c.WordCount, c.FetchError)
	}

	for _, t := range payload.ItemTags {
		newID, ok := itemIDMap[int64(t.ItemID)]
		if !ok {
			continue
		}
		tx.Exec(`INSERT OR IGNORE INTO item_tags (item_id, tag) VALUES (?,?)`, newID, t.Tag)
	}

	for _, f := range payload.Feeds {
		tx.Exec("INSERT INTO custom_feeds (title, url, sort, enabled) VALUES (?, ?, ?, ?)", f.Title, f.URL, f.Sort, f.Enabled)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// 快照数据整体替换后重建全文索引
	db.RebuildClipFTS()

	writtenFiles := 0
	if len(payload.Files) > 0 {
		skipPrefixes := []string{"weather/", "weather\\"}
		for relPath, b64Content := range payload.Files {
			relPath = filepath.ToSlash(relPath)
			skip := false
			for _, p := range skipPrefixes {
				if strings.HasPrefix(relPath, p) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			if strings.Contains(relPath, "..") {
				continue
			}
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(relPath), "."))
			if !backupAllowedFileExt[ext] {
				continue
			}
			absPath := filepath.Join(h.cfg.UploadDir, filepath.FromSlash(relPath))
			if !strings.HasPrefix(filepath.Clean(absPath), h.cfg.UploadDir) {
				continue
			}
			content, err := base64.StdEncoding.DecodeString(b64Content)
			if err != nil {
				continue
			}
			if int64(len(content)) > backupMaxFileSize {
				continue
			}
			if ext == "svg" {
				content = sanitizeSVG(content)
			}
			if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
				continue
			}
			if err := os.WriteFile(absPath, content, 0644); err != nil {
				continue
			}
			writtenFiles++
		}
	}

	return map[string]int{
		"settings": len(payload.Settings),
		"groups":   len(payload.Groups),
		"items":    len(payload.Items),
		"feeds":    len(payload.Feeds),
		"tags":     len(payload.ItemTags),
		"files":    writtenFiles,
	}, nil
}

func (h *BackupHandler) Reset(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r, "仅管理员可执行重置") {
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "重置失败：无法开启事务")
		return
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM clip_contents")
	tx.Exec("DELETE FROM item_tags")
	tx.Exec("DELETE FROM clip_requests")
	tx.Exec("DELETE FROM api_tokens")
	tx.Exec("DELETE FROM items")
	tx.Exec("DELETE FROM item_groups")
	tx.Exec("DELETE FROM custom_feeds")
	tx.Exec("DELETE FROM settings")

	defaults := settings.Defaults()
	for k, v := range defaults {
		tx.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", k, v)
	}

	if err := tx.Commit().Error; err != nil {
		Err(w, http.StatusInternalServerError, 1, "重置失败")
		return
	}

	h.clearUploadDirPreserveWeather()
	db.RebuildClipFTS()
	OK(w, nil)
}

func (h *BackupHandler) clearUploadDirPreserveWeather() {
	entries, err := os.ReadDir(h.cfg.UploadDir)
	if err != nil {
		log.Printf("[backup] 读取上传目录失败: %v", err)
		return
	}
	for _, e := range entries {
		if e.Name() == "weather" {
			continue
		}
		fullPath := filepath.Join(h.cfg.UploadDir, e.Name())
		if err := os.RemoveAll(fullPath); err != nil {
			log.Printf("[backup] 删除 %s 失败: %v", fullPath, err)
		}
	}
}

/* ================= 服务端备份（自动 / 手动） ================= */

// backupTypeDir 校验类型并返回对应目录
func (h *BackupHandler) backupTypeDir(kind string) (string, bool) {
	// 用固定常量分支取目录，避免把请求参数拼进路径
	switch kind {
	case "auto":
		return filepath.Join(h.cfg.BackupDir, "auto"), true
	case "manual":
		return filepath.Join(h.cfg.BackupDir, "manual"), true
	default:
		return "", false
	}
}

// listBackupEntries 列出指定类型的备份文件，按修改时间从新到旧
func (h *BackupHandler) listBackupEntries(kind string) []backupFileEntry {
	dir, ok := h.backupTypeDir(kind)
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []backupFileEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, backupFileEntry{
			Name:    e.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Path:    filepath.Join(dir, e.Name()),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})
	return files
}

// pruneBackups 仅保留最新 backupKeepCount 份
func (h *BackupHandler) pruneBackups(kind string) {
	files := h.listBackupEntries(kind)
	for i := backupKeepCount; i < len(files); i++ {
		if err := os.Remove(files[i].Path); err != nil {
			log.Printf("[backup] 删除过期备份失败: %s: %v", files[i].Name, err)
		}
	}
}

// createBackup 生成一份服务端备份（含全部分组、卡片、设置与上传资源），并清理超出保留数量的旧备份
func (h *BackupHandler) createBackup(kind string) (backupFileMeta, error) {
	var meta backupFileMeta
	dir, ok := h.backupTypeDir(kind)
	if !ok {
		return meta, fmt.Errorf("未知备份类型")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return meta, err
	}

	payload, err := h.collectPayload(true)
	if err != nil {
		return meta, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return meta, err
	}

	now := time.Now()
	name := fmt.Sprintf("solarpanel-%s-%s.json", kind, now.Format("20060102-150405.000"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		return meta, err
	}

	h.pruneBackups(kind)

	info, err := os.Stat(path)
	if err != nil {
		return meta, err
	}
	return backupFileMeta{Name: name, Size: info.Size(), Created: info.ModTime().Format("2006-01-02 15:04:05")}, nil
}

// List 返回自动 / 手动两类服务端备份
func (h *BackupHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r, "仅管理员可查看备份列表") {
		return
	}
	toMeta := func(entries []backupFileEntry) []backupFileMeta {
		out := make([]backupFileMeta, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.meta())
		}
		return out
	}
	OK(w, map[string][]backupFileMeta{
		"auto":   toMeta(h.listBackupEntries("auto")),
		"manual": toMeta(h.listBackupEntries("manual")),
	})
}

// Create 手动备份（也供定时任务复用）
func (h *BackupHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r, "仅管理员可创建备份") {
		return
	}
	meta, err := h.createBackup("manual")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "备份失败: "+err.Error())
		return
	}
	OK(w, meta)
}

// resolveBackupFile 防目录穿越：只允许 data/backups/{auto|manual}/solarpanel-{kind}-*.json。
// 文件名不直接拼路径，而是与目录实际条目精确比对后取目录内的真实名称，避免路径注入。
func (h *BackupHandler) resolveBackupFile(kind, name string) (string, error) {
	dir, ok := h.backupTypeDir(kind)
	if !ok {
		return "", fmt.Errorf("未知备份类型")
	}
	want := strings.TrimSpace(name)
	prefix := "solarpanel-" + kind + "-"
	if want == "" || strings.ContainsAny(want, `/\`) ||
		!strings.HasPrefix(want, prefix) || !strings.HasSuffix(want, ".json") {
		return "", fmt.Errorf("备份文件名无效")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("备份文件不存在")
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() != want {
			continue
		}
		return filepath.Join(dir, e.Name()), nil
	}
	return "", fmt.Errorf("备份文件不存在")
}

// Download 下载服务端备份
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r, "仅管理员可下载备份") {
		return
	}
	kind := r.URL.Query().Get("type")
	path, err := h.resolveBackupFile(kind, r.URL.Query().Get("name"))
	if err != nil {
		Fail(w, err.Error())
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		Fail(w, "备份文件不存在")
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(path)))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

// Restore 用服务端备份恢复（与导入相同的整体替换语义）
func (h *BackupHandler) Restore(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r, "仅管理员可恢复备份") {
		return
	}
	var body struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	path, err := h.resolveBackupFile(body.Type, body.Name)
	if err != nil {
		Fail(w, err.Error())
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		Fail(w, "备份文件不存在")
		return
	}
	var payload backupPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		Fail(w, "备份文件内容无效")
		return
	}
	counts, err := h.applyPayload(payload)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "恢复失败: "+err.Error())
		return
	}
	OK(w, counts)
}

// Delete 删除指定服务端备份
func (h *BackupHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r, "仅管理员可删除备份") {
		return
	}
	var body struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	path, err := h.resolveBackupFile(body.Type, body.Name)
	if err != nil {
		Fail(w, err.Error())
		return
	}
	if err := os.Remove(path); err != nil {
		Fail(w, "备份文件不存在")
		return
	}
	OK(w, nil)
}

/* ================= 自动备份调度 ================= */

// StartAutoBackup 启动后台调度：每 10 分钟检查一次是否到备份时间
func (h *BackupHandler) StartAutoBackup() {
	h.MaybeAutoBackup()
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		h.MaybeAutoBackup()
	}
}

// MaybeAutoBackup 自动备份开关开启且距上次备份超过设定间隔时，生成一份自动备份
func (h *BackupHandler) MaybeAutoBackup() {
	var enabled, intervalRaw string
	db.DB.QueryRow("SELECT config_value FROM settings WHERE config_name='auto_backup_enabled'").Scan(&enabled)
	if enabled != "1" {
		return
	}
	db.DB.QueryRow("SELECT config_value FROM settings WHERE config_name='auto_backup_interval'").Scan(&intervalRaw)

	days := 3
	if n, err := strconv.Atoi(intervalRaw); err == nil {
		switch n {
		case 1, 3, 5, 7, 9:
			days = n
		}
	}

	files := h.listBackupEntries("auto")
	if len(files) > 0 && time.Since(files[0].ModTime) < time.Duration(days)*24*time.Hour {
		return
	}

	meta, err := h.createBackup("auto")
	if err != nil {
		log.Printf("[backup] 自动备份失败: %v", err)
		return
	}
	log.Printf("[backup] 已生成自动备份: %s", meta.Name)
}
