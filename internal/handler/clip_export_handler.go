package handler

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"solarpanel/internal/db"
)

// Export GET /api/v1/clips/export：按当前筛选条件导出剪藏（Markdown + CSV 索引）
func (h *ClipHandler) Export(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}

	where, args := clipExportWhere(r)
	base := ` FROM items i
		LEFT JOIN clip_contents c ON c.item_id = i.id
		LEFT JOIN item_groups g ON g.id = i.group_id
		WHERE ` + strings.Join(where, " AND ")

	rows, err := db.DB.Query(`SELECT i.id, COALESCE(i.title,''), COALESCE(i.url,''), COALESCE(g.title,''),
		COALESCE(c.content_md,''), COALESCE(c.summary,''), COALESCE(c.excerpt,''), COALESCE(i.description,''),
		COALESCE(i.created_at,''), COALESCE(i.clip_read_at,''), COALESCE(i.clip_starred,0), COALESCE(i.clip_source,'')
			`+base+` ORDER BY i.created_at DESC, i.id DESC`, args...)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取剪藏失败")
		return
	}
	defer rows.Close()

	tags := loadClipTagsForExport()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="solarpanel-clips-%s.zip"`, time.Now().Format("20060102-150405")))
	w.Header().Set("Cache-Control", "no-store")

	zw := zip.NewWriter(w)
	defer zw.Close()

	var index bytes.Buffer
	index.WriteString("\ufeff") // Excel 打开 CSV 时避免中文乱码
	cw := csv.NewWriter(&index)
	_ = cw.Write([]string{"id", "title", "url", "group", "tags", "state", "starred", "created_at", "summary", "file"})

	for rows.Next() {
		var (
			id                                    int
			title, url, group, content, summary   string
			excerpt, description, created, readAt string
			starred                               int
			source                                string
		)
		if err := rows.Scan(&id, &title, &url, &group, &content, &summary, &excerpt, &description,
			&created, &readAt, &starred, &source); err != nil {
			continue
		}
		name := clipMarkdownName(id, created, title)
		entry, err := zw.Create(name)
		if err != nil {
			continue
		}
		_, _ = entry.Write([]byte(clipMarkdown(
			id, title, url, group, summary, excerpt, description,
			content, created, readAt, starred == 1, source, tags[id],
		)))

		state := "未读"
		if readAt != "" {
			state = "已读"
		}
		_ = cw.Write([]string{
			strconv.Itoa(id), title, url, group, strings.Join(tags[id], ", "), state,
			strconv.FormatBool(starred == 1), created, summary, name,
		})
	}
	cw.Flush()

	entry, err := zw.Create("clips.csv")
	if err == nil {
		_, _ = entry.Write(index.Bytes())
	}
}

// Duplicates GET /api/v1/clips/duplicates：按规范化 URL 扫描已有重复剪藏
func (h *ClipHandler) Duplicates(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}

	keyRows, err := db.DB.Query(`SELECT url_key, COUNT(*) AS c FROM items
		WHERE clip_source <> '' AND url_key <> ''
		GROUP BY url_key HAVING COUNT(*) > 1
		ORDER BY c DESC, url_key ASC LIMIT 300`)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "扫描重复剪藏失败")
		return
	}
	defer keyRows.Close()

	keys := make([]string, 0, 64)
	for keyRows.Next() {
		var key string
		var count int
		if keyRows.Scan(&key, &count) == nil && key != "" {
			keys = append(keys, key)
		}
	}

	result := map[string]any{
		"groups":           []clipDuplicateGroup{},
		"total_groups":     0,
		"total_duplicates": 0,
	}
	if len(keys) == 0 {
		OK(w, result)
		return
	}

	placeholders := make([]string, len(keys))
	args := make([]any, len(keys))
	for i, key := range keys {
		placeholders[i] = "?"
		args[i] = key
	}

	rows, err := db.DB.Query(`SELECT i.id, i.url_key, COALESCE(i.title,''), COALESCE(i.url,''),
		COALESCE(g.title,''), COALESCE(i.created_at,''), COALESCE(i.clip_read_at,''),
		(COALESCE(c.content_md,'') <> '')
		FROM items i
		LEFT JOIN item_groups g ON g.id = i.group_id
		LEFT JOIN clip_contents c ON c.item_id = i.id
		WHERE i.clip_source <> '' AND i.url_key IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY i.url_key ASC, i.created_at DESC, i.id DESC`, args...)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "扫描重复剪藏失败")
		return
	}
	defer rows.Close()

	groups := make(map[string]*clipDuplicateGroup, len(keys))
	order := make([]string, 0, len(keys))
	for rows.Next() {
		var (
			id              int
			key, title, url string
			group, created  string
			readAt          string
			hasSnapshot     bool
		)
		if err := rows.Scan(&id, &key, &title, &url, &group, &created, &readAt, &hasSnapshot); err != nil {
			continue
		}
		g, ok := groups[key]
		if !ok {
			g = &clipDuplicateGroup{Key: key, URL: url, Items: []clipDuplicateItem{}}
			groups[key] = g
			order = append(order, key)
		}
		if g.URL == "" {
			g.URL = url
		}
		g.Items = append(g.Items, clipDuplicateItem{
			ID:          id,
			Title:       title,
			URL:         url,
			Group:       group,
			CreatedAt:   created,
			ReadAt:      readAt,
			HasSnapshot: hasSnapshot,
		})
	}

	list := make([]clipDuplicateGroup, 0, len(order))
	totalDuplicates := 0
	for _, key := range order {
		g := groups[key]
		if g == nil || len(g.Items) < 2 {
			continue
		}
		g.Count = len(g.Items)
		totalDuplicates += g.Count - 1
		list = append(list, *g)
	}

	result["groups"] = list
	result["total_groups"] = len(list)
	result["total_duplicates"] = totalDuplicates
	OK(w, result)
}

type clipDuplicateGroup struct {
	Key   string              `json:"key"`
	URL   string              `json:"url"`
	Count int                 `json:"count"`
	Items []clipDuplicateItem `json:"items"`
}

type clipDuplicateItem struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Group       string `json:"group"`
	CreatedAt   string `json:"created_at"`
	ReadAt      string `json:"read_at"`
	HasSnapshot bool   `json:"has_snapshot"`
}

// clipExportWhere 复用剪藏列表的筛选参数（不含分页与排序）
func clipExportWhere(r *http.Request) ([]string, []any) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 120 {
		q = string([]rune(q)[:120])
	}
	groupID, _ := strconv.Atoi(r.URL.Query().Get("group"))
	tag := strings.TrimSpace(strings.TrimPrefix(r.URL.Query().Get("tag"), "#"))
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	state := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("state")))

	where := []string{"i.clip_source <> ''"}
	args := []any{}
	if groupID > 0 {
		where = append(where, "i.group_id = ?")
		args = append(args, groupID)
	}
	if tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM item_tags t WHERE t.item_id = i.id AND t.tag = ?)")
		args = append(args, tag)
	}
	if source != "" {
		where = append(where, "i.clip_source = ?")
		args = append(args, source)
	}
	switch kind {
	case "snapshot":
		where = append(where, "COALESCE(c.content_md,'') <> ''")
	case "shot":
		where = append(where, "COALESCE(c.screenshot_path,'') <> ''")
	case "card":
		where = append(where, "COALESCE(c.content_md,'') = '' AND COALESCE(c.screenshot_path,'') = ''")
	case "failed":
		where = append(where, "COALESCE(c.fetch_error,'') <> ''")
	}
	switch state {
	case "unread":
		where = append(where, "i.clip_read_at = ''")
	case "read":
		where = append(where, "i.clip_read_at <> ''")
	case "starred":
		where = append(where, "i.clip_starred = 1")
	}

	if q != "" {
		terms := strings.Fields(q)
		if len(terms) > 8 {
			terms = terms[:8]
		}
		ftsTerms, shortTerms := clipQuerySplit(terms)
		if len(ftsTerms) > 0 {
			where = append(where, "i.id IN (SELECT rowid FROM clip_fts WHERE clip_fts MATCH ?)")
			args = append(args, clipFTSMatch(ftsTerms))
		}
		if f, a := clipShortTermFilter(shortTerms); f != "" {
			where = append(where, f)
			args = append(args, a...)
		}
	}
	return where, args
}

func loadClipTagsForExport() map[int][]string {
	tags := map[int][]string{}
	rows, err := db.DB.Query(`SELECT t.item_id, t.tag FROM item_tags t
		JOIN items i ON i.id = t.item_id
		WHERE i.clip_source <> '' ORDER BY t.item_id ASC, t.tag ASC`)
	if err != nil {
		return tags
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var tag string
		if rows.Scan(&id, &tag) == nil && tag != "" {
			tags[id] = append(tags[id], tag)
		}
	}
	return tags
}

func clipMarkdownName(id int, created, title string) string {
	date := "0000-00-00"
	if len(created) >= 10 {
		date = sanitizeFilename(created[:10])
	}
	slug := sanitizeFilename(title)
	if slug == "" {
		slug = "clip"
	}
	return fmt.Sprintf("%s-%d-%s.md", date, id, slug)
}

func sanitizeFilename(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	count := 0
	lastDash := false
	for _, r := range s {
		if count >= 60 {
			break
		}
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|', '\r', '\n', '\t':
			if !lastDash {
				b.WriteString("-")
				lastDash = true
			}
			continue
		}
		b.WriteRune(r)
		lastDash = false
		count++
	}
	out := strings.Trim(b.String(), "-. ")
	if out == "" {
		return ""
	}
	return out
}

func clipMarkdown(id int, title, url, group, summary, excerpt, description, content, created, readAt string,
	starred bool, source string, tags []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: \"%s\"\n", mdEscape(title))
	fmt.Fprintf(&b, "url: \"%s\"\n", mdEscape(url))
	fmt.Fprintf(&b, "group: \"%s\"\n", mdEscape(group))
	if len(tags) > 0 {
		quoted := make([]string, 0, len(tags))
		for _, tag := range tags {
			quoted = append(quoted, `"`+mdEscape(tag)+`"`)
		}
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(quoted, ", "))
	} else {
		b.WriteString("tags: []\n")
	}
	fmt.Fprintf(&b, "created: \"%s\"\n", mdEscape(created))
	fmt.Fprintf(&b, "source: \"%s\"\n", mdEscape(source))
	if readAt != "" {
		b.WriteString("status: \"read\"\n")
	} else {
		b.WriteString("status: \"unread\"\n")
	}
	fmt.Fprintf(&b, "starred: %t\n", starred)
	if summary != "" {
		fmt.Fprintf(&b, "summary: \"%s\"\n", mdEscape(summary))
	}
	b.WriteString("---\n\n")

	body := strings.TrimSpace(content)
	if body == "" {
		body = strings.TrimSpace(excerpt)
	}
	if body == "" {
		body = strings.TrimSpace(description)
	}
	if body == "" {
		fmt.Fprintf(&b, "> 暂无正文快照，[查看原文](%s)。\n", url)
	} else {
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

func mdEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
