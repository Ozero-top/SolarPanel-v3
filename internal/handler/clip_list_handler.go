package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"solarpanel/internal/db"
)

// clipListItem 剪藏库列表行（只带列表需要的字段，正文用 excerpt / summary 代替）
type clipListItem struct {
	ID             int      `json:"id"`
	GroupID        int      `json:"group_id"`
	Group          string   `json:"group"`
	Title          string   `json:"title"`
	URL            string   `json:"url"`
	Description    string   `json:"description"`
	IconType       string   `json:"icon_type"`
	IconValue      string   `json:"icon_value"`
	Summary        string   `json:"summary"`
	Excerpt        string   `json:"excerpt"`
	WordCount      int      `json:"word_count"`
	FetchedAt      string   `json:"fetched_at"`
	ScreenshotPath string   `json:"screenshot_path"`
	ScreenshotMeta string   `json:"screenshot_meta"`
	Tags           []string `json:"tags"`
	Source         string   `json:"source"`
	ReadAt         string   `json:"read_at"`
	Starred        bool     `json:"starred"`
	CreatedAt      string   `json:"created_at"`
	HasSnapshot    bool     `json:"has_snapshot"`
}

// clipLibraryStats 剪藏库顶部统计
type clipLibraryStats struct {
	Total      int `json:"total"`
	Unread     int `json:"unread"`
	Starred    int `json:"starred"`
	Snapshot   int `json:"snapshot"`
	Screenshot int `json:"screenshot"`
}

// clipTagFacet 标签分面
type clipTagFacet struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// clipGroupFacet 分组分面
type clipGroupFacet struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
}

// List GET /api/v1/clips：剪藏库列表（搜索 / 分组 / 标签 / 状态 / 类型 / 排序 / 分页）
func (h *ClipHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 120 {
		q = string([]rune(q)[:120])
	}
	limit := 24
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 60 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	groupID, _ := strconv.Atoi(r.URL.Query().Get("group"))
	tag := strings.TrimSpace(strings.TrimPrefix(r.URL.Query().Get("tag"), "#"))
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	state := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("state")))
	sortBy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort")))

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
		if clipFTSAvailable() && allClipTermsIndexable(terms) {
			match := make([]string, 0, len(terms))
			for _, t := range terms {
				match = append(match, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
			}
			where = append(where, "i.id IN (SELECT rowid FROM clip_fts WHERE clip_fts MATCH ?)")
			args = append(args, strings.Join(match, " AND "))
		} else {
			for _, t := range terms {
				like := "%" + t + "%"
				where = append(where, `(i.title LIKE ? OR i.url LIKE ? OR i.description LIKE ?
					OR COALESCE(c.excerpt,'') LIKE ? OR COALESCE(c.content_md,'') LIKE ?
					OR EXISTS (SELECT 1 FROM item_tags t WHERE t.item_id = i.id AND t.tag LIKE ?))`)
				args = append(args, like, like, like, like, like, like)
			}
		}
	}

	order := "i.created_at DESC, i.id DESC"
	switch sortBy {
	case "oldest":
		order = "i.created_at ASC, i.id ASC"
	case "updated":
		order = "COALESCE(NULLIF(c.fetched_at,''), i.created_at) DESC, i.id DESC"
	case "words":
		order = "COALESCE(c.word_count,0) DESC, i.id DESC"
	case "title":
		order = "i.title COLLATE NOCASE ASC, i.id ASC"
	case "shot":
		order = "(COALESCE(c.screenshot_path,'') <> '') DESC, i.id DESC"
	}

	base := ` FROM items i
		LEFT JOIN clip_contents c ON c.item_id = i.id
		LEFT JOIN item_groups g ON g.id = i.group_id
		WHERE ` + strings.Join(where, " AND ")

	var total int
	if err := db.DB.QueryRow(`SELECT COUNT(*)`+base, args...).Scan(&total); err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取剪藏失败")
		return
	}

	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := db.DB.Query(`SELECT i.id, i.group_id, COALESCE(g.title,''), COALESCE(i.title,''), COALESCE(i.url,''),
		COALESCE(i.description,''), COALESCE(i.icon_type,''), COALESCE(i.icon_value,''),
		COALESCE(c.summary,''), COALESCE(c.excerpt,''), COALESCE(c.word_count,0), COALESCE(c.fetched_at,''),
		COALESCE(c.screenshot_path,''), COALESCE(c.screenshot_meta,''), COALESCE(i.clip_source,''),
		COALESCE(i.clip_read_at,''), COALESCE(i.clip_starred,0), COALESCE(i.created_at,''),
		(COALESCE(c.content_md,'') <> '')`+base+` ORDER BY `+order+` LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取剪藏失败")
		return
	}
	defer rows.Close()

	list := []clipListItem{}
	ids := []int{}
	for rows.Next() {
		var it clipListItem
		var starred int
		if err := rows.Scan(&it.ID, &it.GroupID, &it.Group, &it.Title, &it.URL, &it.Description,
			&it.IconType, &it.IconValue, &it.Summary, &it.Excerpt, &it.WordCount, &it.FetchedAt,
			&it.ScreenshotPath, &it.ScreenshotMeta, &it.Source, &it.ReadAt, &starred, &it.CreatedAt,
			&it.HasSnapshot); err != nil {
			continue
		}
		it.Starred = starred == 1
		it.Tags = []string{}
		list = append(list, it)
		ids = append(ids, it.ID)
	}
	rows.Close()

	tags := loadTagsForItems(ids)
	for i := range list {
		if t, ok := tags[list[i].ID]; ok {
			list[i].Tags = t
		}
	}

	stats := clipLibraryStats{}
	db.DB.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN i.clip_read_at = '' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN i.clip_starred = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN COALESCE(c.content_md,'') <> '' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN COALESCE(c.screenshot_path,'') <> '' THEN 1 ELSE 0 END), 0)
		FROM items i LEFT JOIN clip_contents c ON c.item_id = i.id
		WHERE i.clip_source <> ''`).Scan(&stats.Total, &stats.Unread, &stats.Starred, &stats.Snapshot, &stats.Screenshot)

	groups := []clipGroupFacet{}
	if rows, err := db.DB.Query(`SELECT i.group_id, COALESCE(g.title,'未分组'), COUNT(*)
		FROM items i LEFT JOIN item_groups g ON g.id = i.group_id
		WHERE i.clip_source <> '' GROUP BY i.group_id, g.title
		ORDER BY COUNT(*) DESC, g.title ASC`); err == nil {
		for rows.Next() {
			var f clipGroupFacet
			if rows.Scan(&f.ID, &f.Title, &f.Count) == nil {
				groups = append(groups, f)
			}
		}
		rows.Close()
	}

	tagFacets := []clipTagFacet{}
	if rows, err := db.DB.Query(`SELECT t.tag, COUNT(*) FROM item_tags t
		JOIN items i ON i.id = t.item_id
		WHERE i.clip_source <> '' GROUP BY t.tag
		ORDER BY COUNT(*) DESC, t.tag ASC LIMIT 40`); err == nil {
		for rows.Next() {
			var f clipTagFacet
			if rows.Scan(&f.Tag, &f.Count) == nil {
				tagFacets = append(tagFacets, f)
			}
		}
		rows.Close()
	}

	OK(w, map[string]any{
		"items":  list,
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"stats":  stats,
		"groups": groups,
		"tags":   tagFacets,
	})
}

// Update PATCH /api/v1/clips/{id}：切换已读 / 收藏（仅本人或管理员）
func (h *ClipHandler) Update(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
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
	if p.Role != "admin" && it.UserID != int(p.UID) {
		Err(w, http.StatusForbidden, 1, "只能修改自己的剪藏")
		return
	}
	var body struct {
		Read    *bool `json:"read"`
		Starred *bool `json:"starred"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if body.Read == nil && body.Starred == nil {
		Fail(w, "没有需要更新的字段")
		return
	}
	if body.Read != nil {
		readAt := ""
		if *body.Read {
			readAt = db.NowStr()
		}
		if _, err := db.DB.Exec(`UPDATE items SET clip_read_at = ? WHERE id = ?`, readAt, id); err != nil {
			Err(w, http.StatusInternalServerError, 1, "更新失败")
			return
		}
	}
	if body.Starred != nil {
		starred := 0
		if *body.Starred {
			starred = 1
		}
		if _, err := db.DB.Exec(`UPDATE items SET clip_starred = ? WHERE id = ?`, starred, id); err != nil {
			Err(w, http.StatusInternalServerError, 1, "更新失败")
			return
		}
	}
	readAt, starred := "", false
	var readAtRaw string
	var starredRaw int
	db.DB.QueryRow(`SELECT COALESCE(clip_read_at,''), COALESCE(clip_starred,0) FROM items WHERE id = ?`, id).Scan(&readAtRaw, &starredRaw)
	readAt = readAtRaw
	starred = starredRaw == 1
	OK(w, map[string]any{"id": id, "read_at": readAt, "starred": starred})
}
