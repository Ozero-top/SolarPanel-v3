package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
	"solarpanel/internal/urlutil"
)

// clipContentRow 正文快照（clip_contents）对外结构
type clipContentRow struct {
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

// clipSearchHit 检索结果行
type clipSearchHit struct {
	ID        int      `json:"id"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Group     string   `json:"group"`
	Excerpt   string   `json:"excerpt"`
	Snippet   string   `json:"snippet"`
	WordCount int      `json:"word_count"`
	FetchedAt string   `json:"fetched_at"`
	Tags      []string `json:"tags"`
}

var clipFTS struct {
	once sync.Once
	ok   bool
}

// clipFTSAvailable 探测 FTS5 虚表是否可用（迁移失败时退回 LIKE 检索）
func clipFTSAvailable() bool {
	clipFTS.once.Do(func() {
		if db.DB == nil {
			return
		}
		var n int
		if err := db.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='clip_fts'`).Scan(&n); err == nil && n > 0 {
			clipFTS.ok = true
		}
	})
	return clipFTS.ok
}

// clipCaptureMode 保存方式：显式请求值优先，其次站点默认，最后卡片模式
func clipCaptureMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "snapshot":
		return "snapshot"
	case "card":
		return "card"
	}
	if db.GetSetting("clip_default_capture") == "snapshot" {
		return "snapshot"
	}
	return "card"
}

// saveClipSnapshot 抓取正文并写入快照表 + FTS 索引，返回 content_hash 与失败原因
func saveClipSnapshot(itemID int, urlKey, rawURL, source, title, coverPath string, page *clipPage) (string, string) {
	if page == nil || page.FetchError != "" || page.HTML == "" {
		return "", "fetch_failed"
	}
	art := extractClipArticle(page.HTML, page.FinalURL)
	if art.Error != "" {
		return "", "extract_failed"
	}
	_, err := db.DB.Exec(`INSERT OR REPLACE INTO clip_contents
		(item_id, url_key, url_original, source, excerpt, content_md, cover_path,
		 fetched_at, http_status, content_hash, word_count, fetch_error)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,'')`,
		itemID, urlKey, rawURL, source, art.Excerpt, art.Markdown, coverPath,
		db.NowStr(), page.HTTPStatus, art.Hash, art.WordCount)
	if err != nil {
		return "", "save_failed"
	}
	indexClipFTS(itemID, title, art.Excerpt, art.Markdown)
	return art.Hash, ""
}

// indexClipFTS 同步单条快照的全文索引（先删后插，保证标题/标签变更也能刷新）
func indexClipFTS(itemID int, title, excerpt, contentMD string) {
	if !clipFTSAvailable() {
		return
	}
	db.DB.Exec(`DELETE FROM clip_fts WHERE rowid = ?`, itemID)
	db.DB.Exec(`INSERT INTO clip_fts(rowid, title, excerpt, content_md, tags) VALUES (?,?,?,?,?)`,
		itemID, title, excerpt, contentMD, strings.Join(loadItemTags(itemID), " "))
}

// dropClipSnapshot 删除快照与索引（卡片本身保留）
func dropClipSnapshot(itemID int) {
	db.DB.Exec(`DELETE FROM clip_contents WHERE item_id = ?`, itemID)
	if clipFTSAvailable() {
		db.DB.Exec(`DELETE FROM clip_fts WHERE rowid = ?`, itemID)
	}
}

func loadClipContent(itemID int) (*clipContentRow, error) {
	var c clipContentRow
	err := db.DB.QueryRow(`SELECT item_id, url_key, url_original, source, excerpt, content_md,
		cover_path, fetched_at, http_status, content_hash, word_count, fetch_error
		FROM clip_contents WHERE item_id = ?`, itemID).Scan(
		&c.ItemID, &c.URLKey, &c.URLOriginal, &c.Source, &c.Excerpt, &c.ContentMD,
		&c.CoverPath, &c.FetchedAt, &c.HTTPStatus, &c.ContentHash, &c.WordCount, &c.FetchError)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Detail GET /api/v1/clips/{id}：卡片详情 + 正文快照
func (h *ClipHandler) Detail(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		Fail(w, "参数错误")
		return
	}
	it, err := loadItem(id)
	if err != nil {
		Err(w, http.StatusNotFound, 1, "剪藏不存在")
		return
	}
	var group string
	db.DB.QueryRow("SELECT title FROM item_groups WHERE id = ?", it.GroupID).Scan(&group)
	resp := map[string]any{"item": it, "group": group, "has_snapshot": false, "content": nil, "tags": loadItemTags(id)}
	if c, err := loadClipContent(id); err == nil {
		resp["has_snapshot"] = c.ContentMD != ""
		resp["content"] = c
	}
	OK(w, resp)
}

// Refetch POST /api/v1/clips/{id}/refetch：重新抓取正文，对比内容是否变化
func (h *ClipHandler) Refetch(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !clipEnabled() {
		Err(w, http.StatusForbidden, 1, "站点已关闭剪藏功能")
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		Fail(w, "参数错误")
		return
	}
	it, err := loadItem(id)
	if err != nil {
		Err(w, http.StatusNotFound, 1, "剪藏不存在")
		return
	}
	target := it.URL
	old, _ := loadClipContent(id)
	if old != nil && old.URLOriginal != "" {
		target = old.URLOriginal
	}
	key := urlutil.Key(target)
	if old != nil && old.URLKey != "" {
		key = old.URLKey
	}
	page := fetchClipPage(target)
	if page.FetchError != "" {
		OK(w, map[string]any{"changed": false, "warnings": []string{"fetch_failed"}, "message": page.FetchError})
		return
	}
	art := extractClipArticle(page.HTML, page.FinalURL)
	if art.Error != "" {
		OK(w, map[string]any{"changed": false, "warnings": []string{"extract_failed"}, "message": art.Error})
		return
	}
	changed := old == nil || old.ContentHash != art.Hash
	coverPath := ""
	if old != nil {
		coverPath = old.CoverPath
	}
	_, err = db.DB.Exec(`INSERT OR REPLACE INTO clip_contents
		(item_id, url_key, url_original, source, excerpt, content_md, cover_path,
		 fetched_at, http_status, content_hash, word_count, fetch_error)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,'')`,
		id, key, target, "refetch",
		art.Excerpt, art.Markdown, coverPath, db.NowStr(), page.HTTPStatus, art.Hash, art.WordCount)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "保存快照失败")
		return
	}
	indexClipFTS(id, it.Title, art.Excerpt, art.Markdown)
	auth.AuditLog(w, r, "clip.refetch", fmt.Sprintf("item:%d", id), "success", clipActor(p), p.Role,
		fmt.Sprintf("changed=%v words=%d", changed, art.WordCount))
	OK(w, map[string]any{
		"changed":      changed,
		"content_hash": art.Hash,
		"word_count":   art.WordCount,
		"fetched_at":   db.NowStr(),
		"warnings":     []string{},
	})
}

// DeleteContent DELETE /api/v1/clips/{id}/content：删除正文快照，保留卡片
func (h *ClipHandler) DeleteContent(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		Fail(w, "参数错误")
		return
	}
	if _, err := loadItem(id); err != nil {
		Err(w, http.StatusNotFound, 1, "剪藏不存在")
		return
	}
	dropClipSnapshot(id)
	auth.AuditLog(w, r, "clip.snapshot.delete", fmt.Sprintf("item:%d", id), "success", clipActor(p), p.Role, "")
	OK(w, map[string]any{"id": id})
}

// Search GET /api/v1/clips/search?q=&limit=&offset=：快照全文检索
func (h *ClipHandler) Search(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	if q == "" {
		OK(w, []clipSearchHit{})
		return
	}
	terms := strings.Fields(q)
	if len(terms) > 8 {
		terms = terms[:8]
	}

	useFTS := clipFTSAvailable() && allClipTermsIndexable(terms)
	var (
		rows *sql.Rows
		err  error
	)
	if useFTS {
		match := make([]string, 0, len(terms))
		for _, t := range terms {
			match = append(match, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		}
		rows, err = db.DB.Query(`SELECT c.item_id, COALESCE(i.title,''), COALESCE(i.url,''), COALESCE(g.title,''),
			c.excerpt, c.content_md, c.word_count, c.fetched_at,
			COALESCE((SELECT group_concat(t.tag, ',') FROM item_tags t WHERE t.item_id = c.item_id), '')
			FROM clip_fts f
			JOIN clip_contents c ON c.item_id = f.rowid
			LEFT JOIN items i ON i.id = c.item_id
			LEFT JOIN item_groups g ON g.id = i.group_id
			WHERE clip_fts MATCH ?
			ORDER BY bm25(clip_fts) ASC, c.fetched_at DESC
			LIMIT ? OFFSET ?`, strings.Join(match, " AND "), limit, offset)
	} else {
		where := make([]string, 0, len(terms))
		args := make([]any, 0, len(terms)*4+2)
		for _, t := range terms {
			where = append(where, "(i.title LIKE ? OR c.excerpt LIKE ? OR c.content_md LIKE ? OR EXISTS (SELECT 1 FROM item_tags t WHERE t.item_id = c.item_id AND t.tag LIKE ?))")
			like := "%" + t + "%"
			args = append(args, like, like, like, like)
		}
		args = append(args, limit, offset)
		rows, err = db.DB.Query(`SELECT c.item_id, COALESCE(i.title,''), COALESCE(i.url,''), COALESCE(g.title,''),
			c.excerpt, c.content_md, c.word_count, c.fetched_at,
			COALESCE((SELECT group_concat(t.tag, ',') FROM item_tags t WHERE t.item_id = c.item_id), '')
			FROM clip_contents c
			LEFT JOIN items i ON i.id = c.item_id
			LEFT JOIN item_groups g ON g.id = i.group_id
			WHERE `+strings.Join(where, " AND ")+`
			ORDER BY c.fetched_at DESC, c.item_id DESC
			LIMIT ? OFFSET ?`, args...)
	}
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "检索失败")
		return
	}
	defer rows.Close()

	list := []clipSearchHit{}
	for rows.Next() {
		var hit clipSearchHit
		var contentMD, tagStr string
		if err := rows.Scan(&hit.ID, &hit.Title, &hit.URL, &hit.Group, &hit.Excerpt, &contentMD, &hit.WordCount, &hit.FetchedAt, &tagStr); err != nil {
			continue
		}
		hit.Tags = splitTagList(tagStr)
		hit.Snippet = clipSnippet(contentMD, terms, 160)
		list = append(list, hit)
	}
	OK(w, list)
}

// clipActor 审计日志的操作者显示名：用户名优先，其次昵称
func clipActor(p *clipPrincipal) string {
	if p.Username != "" {
		return p.Username
	}
	return p.Name
}

// allClipTermsIndexable trigram 分词要求每个词至少 3 个字，否则退回 LIKE
func allClipTermsIndexable(terms []string) bool {
	for _, t := range terms {
		if utf8.RuneCountInString(t) < 3 {
			return false
		}
	}
	return true
}

// clipSnippet 取首个命中词附近的纯文本片段，供列表展示
func clipSnippet(md string, terms []string, width int) string {
	text := clipPlainText(md)
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	lower := strings.ToLower(text)
	hit := -1
	for _, t := range terms {
		if i := strings.Index(lower, strings.ToLower(t)); i >= 0 {
			ri := utf8.RuneCountInString(lower[:i])
			if hit == -1 || ri < hit {
				hit = ri
			}
		}
	}
	if hit == -1 {
		hit = 0
	}
	start := hit - width/3
	if start < 0 {
		start = 0
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
		if end-width > 0 {
			start = end - width
		} else {
			start = 0
		}
	}
	s := string(runes[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(runes) {
		s = s + "…"
	}
	return s
}
