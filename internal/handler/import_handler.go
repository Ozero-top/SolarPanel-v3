package handler

import (
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
	"solarpanel/internal/urlutil"
)

// ImportHandler 浏览器书签导入
type ImportHandler struct{}

func NewImportHandler() *ImportHandler { return &ImportHandler{} }

func (h *ImportHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 401, "未登录")
		return
	}
	if sess.Role == "viewer" || sess.Role == "guest" {
		Err(w, http.StatusForbidden, 403, "只读账号不可导入书签")
		return
	}
	action := r.URL.Query().Get("action")
	if action == "" {
		action = r.FormValue("action")
	}
	switch action {
	case "bookmarks":
		h.Bookmarks(w, r)
	case "import":
		h.ImportBookmarks(w, r)
	default:
		Err(w, http.StatusBadRequest, 1, "unknown action: "+action+"，支持 action=bookmarks / import")
	}
}

// bookmarkEntry 一条书签。Top/Sub 对应导航站的两级分组：
// Top 为空时归入「导入书签」；超过两级的子文件夹合并进 Sub。
type bookmarkEntry struct {
	Top   string
	Sub   string
	Title string
	URL   string
}

var (
	// 一次性按文档顺序扫描 H3 / A / DL，靠 DL 栈还原真实的文件夹嵌套
	bookmarkTagRe = regexp.MustCompile(`(?is)<h3\b([^>]*)>(.*?)</h3>|<a\b([^>]*)>(.*?)</a>|</dl\s*>|<dl\b[^>]*>`)
	hrefRe        = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']*)["']`)
	stripTagRe    = regexp.MustCompile(`(?is)<[^>]+>`)
	allowedProto  = regexp.MustCompile(`(?i)^https?://`)
	// Chrome/Edge 的根容器不是真实分组，导入时跳过
	rootFolderNames = map[string]bool{
		"书签栏": true, "收藏夹栏": true, "书签工具栏": true,
		"其他书签": true, "其他收藏夹": true,
		"bookmarks bar": true, "bookmarks toolbar": true, "bookmarks menu": true,
		"other bookmarks": true, "other favorites": true, "favorites bar": true,
		"bookmarks": true,
	}
)

// readEntries 读取上传的书签文件并解析，返回错误文案（成功时为空）
func (h *ImportHandler) readEntries(r *http.Request) ([]bookmarkEntry, string) {
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, "未收到文件"
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil {
		return nil, "读取文件失败"
	}
	return parseBookmarksHTML(string(data)), ""
}

func (h *ImportHandler) Bookmarks(w http.ResponseWriter, r *http.Request) {
	entries, errMsg := h.readEntries(r)
	if errMsg != "" {
		Err(w, http.StatusBadRequest, 1, errMsg)
		return
	}
	if len(entries) == 0 {
		OK(w, map[string]any{"groups": []any{}, "total": 0})
		return
	}

	type group struct {
		Title string
		Count int
	}
	groupMap := make(map[string]int)
	var groupOrder []string

	for _, e := range entries {
		key := e.Top + "\x00" + e.Sub
		if _, ok := groupMap[key]; !ok {
			groupMap[key] = 0
			groupOrder = append(groupOrder, key)
		}
		groupMap[key]++
	}

	var result []group
	var total int
	for _, key := range groupOrder {
		parts := strings.SplitN(key, "\x00", 2)
		title := parts[0]
		if len(parts) > 1 && parts[1] != "" {
			title += " / " + parts[1]
		}
		cnt := groupMap[key]
		result = append(result, group{Title: title, Count: cnt})
		total += cnt
	}

	OK(w, map[string]any{"groups": result, "total": total})
}

// ImportBookmarks 把解析出的书签真正写入数据库：
// 分组按 Chrome 的两级结构落库（更深层合并），同组内 URL 重复则跳过。
func (h *ImportHandler) ImportBookmarks(w http.ResponseWriter, r *http.Request) {
	entries, errMsg := h.readEntries(r)
	if errMsg != "" {
		Err(w, http.StatusBadRequest, 1, errMsg)
		return
	}
	if len(entries) == 0 {
		OK(w, map[string]any{"imported": 0, "skipped": 0, "groups_created": 0, "mode": "merge"})
		return
	}

	// merge=增量导入（保留现有数据）；overwrite=覆盖导入（先清空分组与卡片）
	mode := strings.ToLower(strings.TrimSpace(r.FormValue("mode")))
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "overwrite" {
		Err(w, http.StatusBadRequest, 1, "导入模式无效，仅支持 merge / overwrite")
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "导入失败")
		return
	}
	defer tx.Rollback()

	if mode == "overwrite" {
		if _, err := tx.Exec("DELETE FROM items"); err != nil {
			Err(w, http.StatusInternalServerError, 1, "清空现有卡片失败")
			return
		}
		if _, err := tx.Exec("DELETE FROM item_groups"); err != nil {
			Err(w, http.StatusInternalServerError, 1, "清空现有分组失败")
			return
		}
	}

	// 现有分组：parent + title → id
	groupIDs := map[string]int64{}
	rows, err := tx.Query("SELECT id, parent_id, title FROM item_groups")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取分组失败")
		return
	}
	for rows.Next() {
		var id, parent int64
		var title string
		if err := rows.Scan(&id, &parent, &title); err == nil {
			key := groupKey(parent, title)
			if _, ok := groupIDs[key]; !ok {
				groupIDs[key] = id
			}
		}
	}
	rows.Close()

	// 每个父级下已有的最大 sort
	groupSort := map[int64]int{}
	sortRows, err := tx.Query("SELECT parent_id, COALESCE(MAX(sort), 0) FROM item_groups GROUP BY parent_id")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取分组排序失败")
		return
	}
	for sortRows.Next() {
		var parent int64
		var maxSort int
		if err := sortRows.Scan(&parent, &maxSort); err == nil {
			groupSort[parent] = maxSort
		}
	}
	sortRows.Close()

	// 已有卡片按分组去重：不同分组允许同一 URL
	existing := map[int64]map[string]bool{}
	urlRows, err := tx.Query("SELECT group_id, url FROM items WHERE url != ''")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取卡片失败")
		return
	}
	for urlRows.Next() {
		var gid int64
		var u string
		if err := urlRows.Scan(&gid, &u); err == nil {
			if existing[gid] == nil {
				existing[gid] = map[string]bool{}
			}
			existing[gid][u] = true
		}
	}
	urlRows.Close()

	groupStmt, err := tx.Prepare("INSERT INTO item_groups (parent_id, title, sort) VALUES (?, ?, ?)")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "导入失败")
		return
	}
	defer groupStmt.Close()
	itemStmt, err := tx.Prepare("INSERT INTO items (group_id, title, url, icon_type, icon_value, open_method, sort, url_key) VALUES (?, ?, ?, 'favicon', '', 2, ?, ?)")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "导入失败")
		return
	}
	defer itemStmt.Close()

	itemSort := map[int64]int{}
	groupsCreated, imported, skipped := 0, 0, 0

	ensureGroup := func(parent int64, title string) (int64, bool) {
		key := groupKey(parent, title)
		if id, ok := groupIDs[key]; ok {
			return id, true
		}
		groupSort[parent]++
		res, err := groupStmt.Exec(parent, title, groupSort[parent])
		if err != nil {
			return 0, false
		}
		id, _ := res.LastInsertId()
		groupIDs[key] = id
		groupsCreated++
		return id, true
	}

	for _, e := range entries {
		topTitle := strings.TrimSpace(e.Top)
		if topTitle == "" {
			topTitle = "导入书签"
		}
		gid, ok := ensureGroup(0, topTitle)
		if !ok {
			skipped++
			continue
		}
		if sub := strings.TrimSpace(e.Sub); sub != "" {
			childID, ok := ensureGroup(gid, sub)
			if !ok {
				skipped++
				continue
			}
			gid = childID
		}

		if existing[gid] != nil && existing[gid][e.URL] {
			skipped++
			continue
		}
		sort := itemSort[gid] + 1
		if _, err := itemStmt.Exec(gid, e.Title, e.URL, sort, urlutil.Key(e.URL)); err != nil {
			skipped++
			continue
		}
		if existing[gid] == nil {
			existing[gid] = map[string]bool{}
		}
		existing[gid][e.URL] = true
		itemSort[gid] = sort
		imported++
	}

	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, 1, "导入失败")
		return
	}
	OK(w, map[string]any{"imported": imported, "skipped": skipped, "groups_created": groupsCreated, "mode": mode})
}

func groupKey(parent int64, title string) string {
	return strconv.FormatInt(parent, 10) + "\x00" + title
}

// parseBookmarksHTML 用 DL 栈还原文件夹层级：
// Chrome 导出为 <H3>文件夹</H3><DL>...子项...</DL>，按标签顺序扫描即可正确归属。
func parseBookmarksHTML(content string) []bookmarkEntry {
	s := strings.TrimLeft(content, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var entries []bookmarkEntry
	stack := []string{} // 空字符串代表根容器层级
	pending := ""
	pendingRoot := false

	for _, m := range bookmarkTagRe.FindAllStringSubmatch(s, -1) {
		tag := strings.ToLower(m[0])
		switch {
		case strings.HasPrefix(tag, "<h3"):
			pending = cleanBookmarkText(m[2])
			pendingRoot = strings.Contains(strings.ToUpper(m[1]), "PERSONAL_TOOLBAR_FOLDER")
		case strings.HasPrefix(tag, "<a"):
			hm := hrefRe.FindStringSubmatch(m[3])
			if hm == nil {
				continue
			}
			u := strings.TrimSpace(html.UnescapeString(hm[1]))
			if !allowedProto.MatchString(u) {
				continue
			}
			title := cleanBookmarkText(m[4])
			if title == "" {
				title = u
			}
			top, sub := resolveBookmarkGroup(stack)
			entries = append(entries, bookmarkEntry{Top: top, Sub: sub, Title: title, URL: u})
		case strings.HasPrefix(tag, "</dl"):
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case strings.HasPrefix(tag, "<dl"):
			name := pending
			if pendingRoot || rootFolderNames[strings.ToLower(name)] {
				name = ""
			}
			stack = append(stack, name)
			pending = ""
			pendingRoot = false
		}
	}
	return entries
}

// resolveBookmarkGroup 只取前两级（导航站最多两级），更深层并入第二级
func resolveBookmarkGroup(stack []string) (string, string) {
	var names []string
	for _, n := range stack {
		if n != "" {
			names = append(names, n)
		}
	}
	switch len(names) {
	case 0:
		return "导入书签", ""
	case 1:
		return names[0], ""
	default:
		return names[0], names[1]
	}
}

func cleanBookmarkText(s string) string {
	return strings.TrimSpace(html.UnescapeString(stripTagRe.ReplaceAllString(s, "")))
}
