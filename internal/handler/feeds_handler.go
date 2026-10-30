package handler

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
)

// FeedsHandler 自定义 RSS/Atom 源
type FeedsHandler struct{}

func NewFeedsHandler() *FeedsHandler { return &FeedsHandler{} }

// AppVersion 当前应用版本号（由 main.go 注入）
var AppVersion = ""

var validFeedURLRe = regexp.MustCompile(`^https?://`)

// CustomFeedRow RSS 源
type CustomFeedRow struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Sort     int    `json:"sort"`
	Enabled  int    `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

func (h *FeedsHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if action == "" {
		action = r.FormValue("action")
	}
	switch action {
	case "list", "":
		h.List(w, r)
	case "save":
		h.Save(w, r)
	case "delete":
		h.Delete(w, r)
	case "toggle":
		h.Toggle(w, r)
	default:
		Err(w, http.StatusBadRequest, 1, "unknown action: "+action)
	}
}

func (h *FeedsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query("SELECT id, title, url, sort, enabled, created_at FROM custom_feeds ORDER BY enabled DESC, sort ASC, id ASC")
	if err != nil {
		Fail(w, "查询失败")
		return
	}
	defer rows.Close()
	var feeds []CustomFeedRow
	for rows.Next() {
		var f CustomFeedRow
		rows.Scan(&f.ID, &f.Title, &f.URL, &f.Sort, &f.Enabled, &f.CreatedAt)
		feeds = append(feeds, f)
	}
	if feeds == nil {
		feeds = []CustomFeedRow{}
	}
	OK(w, feeds)
}

func (h *FeedsHandler) Save(w http.ResponseWriter, r *http.Request) {
	if s := auth.GetSessionFromRequest(r); s == nil || (s.Role != "admin" && s.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body map[string]any
	if r.Body != nil && r.ContentLength > 0 {
		body = map[string]any{}
		data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		json.Unmarshal(data, &body)
	}
	get := func(k, def string) string {
		if v, ok := body[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		if v := r.FormValue(k); v != "" {
			return v
		}
		if v := r.URL.Query().Get(k); v != "" {
			return v
		}
		return def
	}
	getInt := func(k string, def int) int {
		if v, ok := body[k]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			case json.Number:
				i, _ := n.Int64()
				return int(i)
			}
		}
		i, _ := strconv.Atoi(r.FormValue(k))
		if i == 0 {
			i, _ = strconv.Atoi(r.URL.Query().Get(k))
		}
		if i == 0 {
			return def
		}
		return i
	}

	id := getInt("id", 0)
	title := get("title", "")
	urlStr := get("url", "")
	sort := getInt("sort", 0)
	enabled := getInt("enabled", 1)

	if title == "" {
		Err(w, http.StatusBadRequest, 1, "标题不能为空")
		return
	}
	if !validFeedURLRe.MatchString(urlStr) {
		Err(w, http.StatusBadRequest, 1, "URL 必须以 http:// 或 https:// 开头")
		return
	}
	parsed, err := url.Parse(urlStr)
	if err != nil || parsed.Hostname() == "" {
		Err(w, http.StatusBadRequest, 1, "URL 格式不合法")
		return
	}
	if isPrivateHost(parsed.Hostname()) {
		Err(w, http.StatusBadRequest, 1, "URL 指向内网/保留地址，已拦截")
		return
	}

	if id > 0 {
		var existing int
		err := db.DB.QueryRow("SELECT id FROM custom_feeds WHERE id=?", id).Scan(&existing)
		if err != nil {
			Err(w, http.StatusNotFound, 1, "RSS 源不存在")
			return
		}
		db.DB.Exec("UPDATE custom_feeds SET title=?, url=?, sort=?, enabled=? WHERE id=?", title, urlStr, sort, enabled, id)
		OK(w, nil)
	} else {
		res, err := db.DB.Exec("INSERT INTO custom_feeds (title, url, sort, enabled) VALUES (?, ?, ?, ?)", title, urlStr, sort, enabled)
		if err != nil {
			Err(w, http.StatusInternalServerError, 1, "保存失败")
			return
		}
		lastID, _ := res.LastInsertId()
		OK(w, CustomFeedRow{ID: int(lastID), Title: title, URL: urlStr, Sort: sort, Enabled: enabled})
	}
}

func (h *FeedsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if s := auth.GetSessionFromRequest(r); s == nil || (s.Role != "admin" && s.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		BindJSON(r, &body)
	}
	if body.ID == 0 {
		body.ID, _ = strconv.Atoi(r.FormValue("id"))
		if body.ID == 0 {
			body.ID, _ = strconv.Atoi(r.URL.Query().Get("id"))
		}
	}
	if body.ID <= 0 {
		Err(w, http.StatusBadRequest, 1, "id 无效")
		return
	}
	db.DB.Exec("DELETE FROM custom_feeds WHERE id=?", body.ID)
	OK(w, nil)
}

func (h *FeedsHandler) Toggle(w http.ResponseWriter, r *http.Request) {
	if s := auth.GetSessionFromRequest(r); s == nil || (s.Role != "admin" && s.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		BindJSON(r, &body)
	}
	if body.ID == 0 {
		body.ID, _ = strconv.Atoi(r.FormValue("id"))
		if body.ID == 0 {
			body.ID, _ = strconv.Atoi(r.URL.Query().Get("id"))
		}
	}
	if body.ID <= 0 {
		Err(w, http.StatusBadRequest, 1, "id 无效")
		return
	}
	var enabled int
	err := db.DB.QueryRow("SELECT enabled FROM custom_feeds WHERE id=?", body.ID).Scan(&enabled)
	if err != nil {
		Err(w, http.StatusNotFound, 1, "RSS 源不存在")
		return
	}
	if enabled == 1 {
		enabled = 0
	} else {
		enabled = 1
	}
	db.DB.Exec("UPDATE custom_feeds SET enabled=? WHERE id=?", enabled, body.ID)
	OK(w, enabled)
}

// ===== NewsHandler 的依赖部分（定义在 feeds_handler.go 以避免循环） =====

type newsSourceMeta struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Type     string `json:"type"`
	Home     string `json:"home"`
	Logo     string `json:"logo"`
	Interval int    `json:"interval"`
}

type newsItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Info  string `json:"info"`
	Time  string `json:"time"`
}

type cacheEntry struct {
	items  []newsItem
	expire time.Time
	setAt  time.Time // 最近一次成功抓取时间（前端"x 分钟前更新"用）
}

var newsCache sync.Map

// 单飞锁：同一数据源同一时刻只真正回源一次，
// 避免"首屏 all"与"逐源刷新"同时打上游（这也是首屏慢和 429 的主因之一）
var newsFetchLocks sync.Map

func newsFetchLock(id string) *sync.Mutex {
	if v, ok := newsFetchLocks.Load(id); ok {
		return v.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := newsFetchLocks.LoadOrStore(id, m)
	return actual.(*sync.Mutex)
}

func cacheGet(srcID string, ttl time.Duration) ([]newsItem, bool) {
	v, ok := newsCache.Load(srcID)
	if !ok {
		return nil, false
	}
	e := v.(cacheEntry)
	if time.Now().After(e.expire) {
		return nil, false
	}
	return e.items, true
}

// cachePeek 取缓存快照（不判断是否过期）：
// ok=true 表示曾有成功抓取，setAt 为写入时间；配合 stale-while-revalidate 先出内容再回源
func cachePeek(srcID string) (items []newsItem, setAt time.Time, ok bool) {
	v, ok := newsCache.Load(srcID)
	if !ok {
		return nil, time.Time{}, false
	}
	e := v.(cacheEntry)
	return e.items, e.setAt, true
}

func cacheStore(srcID string, items []newsItem, ttl time.Duration, setAt time.Time) {
	newsCache.Store(srcID, cacheEntry{items: items, expire: time.Now().Add(ttl), setAt: setAt})
}

func cacheSet(srcID string, items []newsItem, ttl time.Duration) {
	at := time.Now()
	cacheStore(srcID, items, ttl, at)
	persistNewsCache(srcID, items, at.Unix())
}

// ---------- 缓存落盘：重启后首屏直接出上次快照，不必再冷启动 ----------

var newsCacheTableOnce sync.Once

func newsCacheTable() {
	newsCacheTableOnce.Do(func() {
		db.DB.Exec(`CREATE TABLE IF NOT EXISTS news_cache (
			id TEXT PRIMARY KEY,
			payload TEXT NOT NULL,
			updated INTEGER NOT NULL
		)`)
	})
}

// persistNewsCache 异步写入快照（失败静默，纯加速手段，不影响功能）
func persistNewsCache(id string, items []newsItem, updated int64) {
	if len(items) == 0 {
		return
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return
	}
	go func() {
		newsCacheTable()
		db.DB.Exec(`INSERT INTO news_cache (id, payload, updated) VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET payload=excluded.payload, updated=excluded.updated`,
			id, string(payload), updated)
	}()
}

var newsCacheLoadOnce sync.Once

// loadNewsCacheRows 启动后首次访问时把落盘快照装回内存（过期但可先展示，由刷新流程更新）
func loadNewsCacheRows() {
	newsCacheLoadOnce.Do(func() {
		newsCacheTable()
		rows, err := db.DB.Query("SELECT id, payload, updated FROM news_cache")
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id, payload string
			var updated int64
			if rows.Scan(&id, &payload, &updated) != nil {
				continue
			}
			var items []newsItem
			if json.Unmarshal([]byte(payload), &items) != nil {
				continue
			}
			// 按该源自身的 TTL 计算剩余有效期：
			// 重启前刚抓过的源仍然算新鲜（不会被首轮巡检无谓重抓），已过期的则立即触发刷新
			at := time.Unix(updated, 0)
			newsCache.Store(id, cacheEntry{items: items, expire: at.Add(ttlByID(id)), setAt: at})
		}
	})
}

func newsHTTP(method, u string, headers map[string]string, body []byte, timeout time.Duration) (int, []byte, error) {
	client := &http.Client{Timeout: timeout}
	var br io.Reader
	if body != nil {
		br = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, u, br)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	// 上游响应限 10MB，避免异常数据源耗尽内存
	b, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, b, nil
}

var htmlEntityReplacer = strings.NewReplacer(
	"&amp;", "&",
	"&lt;", "<",
	"&gt;", ">",
	"&quot;", "\"",
	"&#39;", "'",
	"&nbsp;", " ",
	"&copy;", "©",
	"&reg;", "®",
	"&trade;", "™",
	"&ldquo;", "\u201c",
	"&rdquo;", "\u201d",
	"&lsquo;", "\u2018",
	"&rsquo;", "\u2019",
	"&mdash;", "\u2014",
	"&ndash;", "\u2013",
)

func htmlUnescape(s string) string {
	return htmlEntityReplacer.Replace(s)
}

func newItem(idPrefix, id, title, u, info string) newsItem {
	if title == "" {
		return newsItem{}
	}
	fullID := idPrefix + "_" + id
	h := md5.Sum([]byte(fullID))
	return newsItem{
		ID:    hex.EncodeToString(h[:]),
		Title: title,
		URL:   u,
		Info:  info,
		Time:  time.Now().Format("2006-01-02 15:04:05"),
	}
}

func prefixIDs(items []newsItem, prefix string) {
	for i := range items {
		h := md5.Sum([]byte(fmt.Sprintf("%s_%d_%s", prefix, i, items[i].Title)))
		items[i].ID = hex.EncodeToString(h[:])
	}
}
