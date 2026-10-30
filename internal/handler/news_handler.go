package handler

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/db"
)

type NewsHandler struct{}

var allNewsSources = []newsSourceMeta{
	{ID: "zhihu", Name: "知乎热榜", Color: "#0084ff", Type: "hottest", Home: "https://www.zhihu.com/hot", Logo: "https://static.zhihu.com/heifetz/favicon.ico", Interval: 600},
	{ID: "baidu", Name: "百度热搜", Color: "#315efb", Type: "hottest", Home: "https://top.baidu.com/board?tab=realtime", Logo: "https://www.baidu.com/favicon.ico", Interval: 600},
	{ID: "bilibili", Name: "哔哩哔哩热门", Color: "#fb7299", Type: "hottest", Home: "https://www.bilibili.com/v/popular/all", Logo: "https://www.bilibili.com/favicon.ico", Interval: 600},
	{ID: "weibo", Name: "微博热搜", Color: "#ef4444", Type: "hottest", Home: "https://s.weibo.com/top/summary", Logo: "https://weibo.com/favicon.ico", Interval: 300},
	{ID: "hackernews", Name: "Hacker News", Color: "#ff6600", Type: "hottest", Home: "https://news.ycombinator.com/", Logo: "https://news.ycombinator.com/favicon.ico", Interval: 900},
	{ID: "github", Name: "GitHub Trending", Color: "#6e7681", Type: "hottest", Home: "https://github.com/trending", Logo: "https://github.githubassets.com/favicons/favicon.svg", Interval: 1800},
	{ID: "thepaper", Name: "澎湃新闻", Color: "#d43c33", Type: "hottest", Home: "https://www.thepaper.cn/", Logo: "https://www.thepaper.cn/favicon.ico", Interval: 900},
	{ID: "toutiao", Name: "今日头条", Color: "#f04142", Type: "hottest", Home: "https://www.toutiao.com/", Logo: "https://www.toutiao.com/favicon.ico", Interval: 600},
	{ID: "tieba", Name: "百度贴吧", Color: "#3177f6", Type: "hottest", Home: "https://tieba.baidu.com/hottopic/browse/topicList", Logo: "https://tieba.baidu.com/favicon.ico", Interval: 900},
	{ID: "douban", Name: "豆瓣电影", Color: "#2e963f", Type: "hottest", Home: "https://movie.douban.com/", Logo: "https://www.douban.com/favicon.ico", Interval: 3600},
	{ID: "hupu", Name: "虎扑", Color: "#c81623", Type: "hottest", Home: "https://bbs.hupu.com/all-gambia", Logo: "https://bbs.hupu.com/favicon.ico", Interval: 900},
	{ID: "juejin", Name: "稀土掘金", Color: "#1e80ff", Type: "hottest", Home: "https://juejin.cn/hot/articles", Logo: "https://juejin.cn/favicon.ico", Interval: 1800},
	{ID: "sspai", Name: "少数派", Color: "#d71a1b", Type: "hottest", Home: "https://sspai.com/", Logo: "https://cdn.sspai.com/sspai/assets/img/favicon/favicon.ico", Interval: 1800},
	{ID: "nowcoder", Name: "牛客", Color: "#45a9f0", Type: "hottest", Home: "https://www.nowcoder.com/discuss", Logo: "https://www.nowcoder.com/favicon.ico", Interval: 900},
	{ID: "tencent", Name: "腾讯新闻", Color: "#147dff", Type: "hottest", Home: "https://news.qq.com/", Logo: "https://news.qq.com/favicon.ico", Interval: 1800},
	{ID: "producthunt", Name: "Product Hunt", Color: "#da552f", Type: "hottest", Home: "https://www.producthunt.com/", Logo: "https://www.producthunt.com/favicon.ico", Interval: 3600},
	{ID: "freebuf", Name: "FreeBuf", Color: "#22a06b", Type: "hottest", Home: "https://www.freebuf.com/", Logo: "https://www.freebuf.com/favicon.ico", Interval: 1800},
	{ID: "ithome", Name: "IT之家", Color: "#d92626", Type: "realtime", Home: "https://www.ithome.com/", Logo: "https://www.ithome.com/favicon.ico", Interval: 600},
	{ID: "solidot", Name: "Solidot", Color: "#0d9488", Type: "realtime", Home: "https://www.solidot.org/", Logo: "https://www.solidot.org/favicon.ico", Interval: 3600},
	{ID: "wallstreetcn", Name: "华尔街见闻", Color: "#cf9b22", Type: "realtime", Home: "https://wallstreetcn.com/live/global", Logo: "https://wallstreetcn.com/favicon.ico", Interval: 600},
	{ID: "v2ex", Name: "V2EX", Color: "#6b7a90", Type: "realtime", Home: "https://www.v2ex.com/", Logo: "https://www.v2ex.com/favicon.ico", Interval: 900},
	{ID: "douyin", Name: "抖音热点", Color: "#000000", Type: "hottest", Home: "https://www.douyin.com/hot", Logo: "https://www.douyin.com/favicon.ico", Interval: 600},
	{ID: "36kr", Name: "36氪热榜", Color: "#00b3ff", Type: "hottest", Home: "https://36kr.com/hot-list/renqi", Logo: "https://36kr.com/favicon.ico", Interval: 1800},
	{ID: "csdn", Name: "CSDN热榜", Color: "#fc5531", Type: "hottest", Home: "https://blog.csdn.net/rank/list", Logo: "https://www.csdn.net/favicon.ico", Interval: 1800},
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}
type rssChannel struct {
	Items []rssItem `xml:"item"`
}
type rssItem struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
}
type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []atomEntry `xml:"entry"`
}
type atomEntry struct {
	Title string   `xml:"title"`
	Link  atomLink `xml:"link"`
}
type atomLink struct {
	Href string `xml:"href,attr"`
}

func parseRSS(body []byte) []newsItem {
	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		var af atomFeed
		if err2 := xml.Unmarshal(body, &af); err2 != nil {
			return nil
		}
		now := time.Now().Format("2006-01-02 15:04:05")
		result := make([]newsItem, 0, len(af.Entries))
		for i, e := range af.Entries {
			title := strings.TrimSpace(e.Title)
			if title == "" {
				continue
			}
			h := md5Hex(fmt.Sprintf("at_%d_%s", i, title))
			result = append(result, newsItem{ID: h, Title: title, URL: e.Link.Href, Time: now})
		}
		return result
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	result := make([]newsItem, 0, len(feed.Channel.Items))
	for i, it := range feed.Channel.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		h := md5Hex(fmt.Sprintf("rs_%d_%s", i, title))
		result = append(result, newsItem{ID: h, Title: title, URL: it.Link, Time: now})
	}
	return result
}

// fetchMap 所有内置热榜源的分发表 —— 加新源只需在这里加一行
var fetchMap = map[string]func() []newsItem{
	"zhihu":         fetchZhihu,
	"baidu":         fetchBaidu,
	"bilibili":      fetchBilibili,
	"weibo":         fetchWeibo,
	"hackernews":    fetchHackerNews,
	"github":        fetchGitHub,
	"thepaper":      fetchThepaper,
	"toutiao":       fetchToutiao,
	"tieba":         fetchTieba,
	"douban":        fetchDouban,
	"hupu":          fetchHupu,
	"juejin":        fetchJuejin,
	"sspai":         fetchSspai,
	"nowcoder":      fetchNowcoder,
	"tencent":       fetchTencent,
	"producthunt":   fetchProductHunt,
	"freebuf":       fetchFreeBuf,
	"ithome":        fetchITHome,
	"solidot":       fetchSolidot,
	"wallstreetcn":  fetchWallstreetcn,
	"v2ex":          fetchV2ex,
	"douyin":        fetchDouyin,
	"36kr":          fetch36Kr,
	"csdn":          fetchCSDN,
}

// runFetch 统一分发入口 —— 失败/成功都打 log 方便定位
func runFetch(src newsSourceMeta) []newsItem {
	fn, ok := fetchMap[src.ID]
	if !ok {
		log.Printf("[news] %s(%s) — unknown source, skipped", src.ID, src.Name)
		return nil
	}
	started := time.Now()
	items := fn()
	elapsed := time.Since(started).Round(time.Millisecond)
	if items == nil {
		log.Printf("[news] %s(%s) — fetch FAILED (nil) in %v", src.ID, src.Name, elapsed)
	} else {
		log.Printf("[news] %s(%s) — OK %d items in %v", src.ID, src.Name, len(items), elapsed)
	}
	return items
}

func fetchCustomFeed(feedURL string) []newsItem {
	u, err := url.Parse(feedURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	if isPrivateHost(u.Hostname()) {
		return nil
	}
	// 自定义源地址来自数据库，使用带内网 IP 拦截的连接器防止 DNS 重绑定 SSRF
	req, err := http.NewRequest("GET", feedURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "SolarPanel/Go")
	resp, err := newSafeHTTPClient(15 * time.Second).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || len(body) == 0 {
		return nil
	}
	return parseRSS(body)
}

func newsTTL(src newsSourceMeta) time.Duration {
	ttl := time.Duration(src.Interval) * time.Second
	if ttl < 30*time.Second {
		ttl = 5 * time.Minute
	}
	return ttl
}

// ttlByID 按 id 取该源的 TTL（落盘快照恢复时用它计算剩余有效期）
func ttlByID(id string) time.Duration {
	if strings.HasPrefix(id, "feed_") {
		return 10 * time.Minute
	}
	for i := range allNewsSources {
		if allNewsSources[i].ID == id {
			return newsTTL(allNewsSources[i])
		}
	}
	return 5 * time.Minute
}

func newsStatus(items []newsItem) string {
	if len(items) == 0 {
		return "empty"
	}
	return "success"
}

// fetchSource 单飞回源：同一数据源的并发请求只会真正抓取一次（force=卡片刷新按钮，跳过缓存）
func fetchSource(src newsSourceMeta, force bool) []newsItem {
	ttl := newsTTL(src)
	mu := newsFetchLock(src.ID)
	mu.Lock()
	defer mu.Unlock()
	if !force {
		if items, ok := cacheGet(src.ID, ttl); ok {
			return items
		}
	}
	items := runFetch(src)
	if items != nil {
		cacheSet(src.ID, items, ttl)
		return items
	}
	// 抓取失败：保留旧快照，只缩短有效期，避免偶发失败把已有内容清空
	if old, at, ok := cachePeek(src.ID); ok && len(old) > 0 {
		cacheStore(src.ID, old, 30*time.Second, at)
		return old
	}
	cacheStore(src.ID, []newsItem{}, time.Minute, time.Now())
	return []newsItem{}
}

// fetchCustomFeedCached 自定义 RSS 源的缓存包装（10 分钟 TTL）
func fetchCustomFeedCached(id, feedURL string, force bool) []newsItem {
	const ttl = 10 * time.Minute
	mu := newsFetchLock(id)
	mu.Lock()
	defer mu.Unlock()
	if !force {
		if items, ok := cacheGet(id, ttl); ok {
			return items
		}
	}
	items := fetchCustomFeed(feedURL)
	if items != nil {
		cacheSet(id, items, ttl)
		return items
	}
	if old, at, ok := cachePeek(id); ok && len(old) > 0 {
		cacheStore(id, old, 30*time.Second, at)
		return old
	}
	cacheStore(id, []newsItem{}, time.Minute, time.Now())
	return []newsItem{}
}

// newsSchedulerInterval 定时巡检间隔；每次巡检只刷新"已过各自 TTL"的源，
// 所以实际更新频率仍是各源自己的 Interval（5/10/15/30/60 分钟）
const newsSchedulerInterval = 60 * time.Second

var newsSchedulerOnce sync.Once

// StartNewsScheduler 启动后台定时刷新：开启 news_auto_refresh 后，
// 即使无人访问页面，数据也会按各源 TTL 自动更新；开关默认关闭，此时后台不产生任何抓取。
func StartNewsScheduler() {
	newsSchedulerOnce.Do(func() {
		go func() {
			time.Sleep(3 * time.Second) // 等 DB / 路由就绪
			loadNewsCacheRows()         // 先把上次落盘快照装回内存
			for {
				refreshStaleNewsOnce()
				time.Sleep(newsSchedulerInterval)
			}
		}()
	})
}

// refreshStaleNewsOnce 单次巡检：把过期（或从未抓过）的源并发刷新一遍
func refreshStaleNewsOnce() {
	// 后台自动刷新开关（news_auto_refresh，默认 "0" 关闭）：
	// 关闭时后台完全不回源，只保留"有人打开新闻视图时按需刷新"的路径
	if getNewsSetting("news_auto_refresh") != "1" {
		return
	}
	enabled, ids, order := newsEnabledSetting()
	if !enabled {
		return
	}
	ordered := orderedSources(ids, order)
	sem := make(chan struct{}, 4) // 巡检并发上限，避免瞬间打满上游
	var wg sync.WaitGroup
	cnt := 0

	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn()
		}()
	}

	for _, src := range ordered {
		if _, ok := cacheGet(src.ID, newsTTL(src)); ok {
			continue // 仍在 TTL 内，跳过
		}
		run(func() { fetchSource(src, false) })
		cnt++
	}

	// 自定义 RSS 源同样按 10 分钟 TTL 巡检（启用/停用实时生效）
	rows, err := db.DB.Query("SELECT id, url FROM custom_feeds WHERE enabled=1")
	if err == nil {
		for rows.Next() {
			var id int
			var u string
			if rows.Scan(&id, &u) != nil || u == "" {
				continue
			}
			sid := "feed_" + strconv.Itoa(id)
			if _, ok := cacheGet(sid, 10*time.Minute); ok {
				continue
			}
			run(func() { fetchCustomFeedCached(sid, u, false) })
			cnt++
		}
		rows.Close()
	}

	wg.Wait()
	if cnt > 0 {
		log.Printf("[news] 定时刷新完成：%d 个过期源已更新", cnt)
	}
}

func getNewsSetting(key string) string { return db.GetSetting(key) }

func parseSources(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		out := make([]string, 0, len(arr))
		for _, s := range arr {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// newsSourcePublic 用于对外序列化的数据源元信息
type newsSourcePublic struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Type     string `json:"type"`
	Home     string `json:"home"`
	Logo     string `json:"logo"`
	Interval int    `json:"interval"`
}

func sourceToPublic(src newsSourceMeta) newsSourcePublic {
	return newsSourcePublic{ID: src.ID, Name: src.Name, Color: src.Color, Type: src.Type, Home: src.Home, Logo: src.Logo, Interval: src.Interval}
}

func orderedSources(ids, order []string) []newsSourceMeta {
	byID := make(map[string]newsSourceMeta, len(allNewsSources))
	for _, s := range allNewsSources {
		byID[s.ID] = s
	}
	enabledFlip := map[string]bool{}
	for _, id := range ids {
		enabledFlip[id] = true
	}
	if len(ids) == 0 {
		enabledFlip = nil
	}
	var out []newsSourceMeta
	seen := map[string]bool{}
	for _, sid := range order {
		s, ok := byID[sid]
		if !ok || seen[sid] {
			continue
		}
		if enabledFlip != nil && !enabledFlip[sid] {
			seen[sid] = true
			continue
		}
		seen[sid] = true
		out = append(out, s)
	}
	for _, s := range allNewsSources {
		if seen[s.ID] {
			continue
		}
		if enabledFlip != nil && !enabledFlip[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	return out
}

func newsEnabledSetting() (bool, []string, []string) {
	// 独立开关：0=整体关闭热点新闻（即使有 sources 勾选也不展示），1=按 news_sources 勾选决定
	if getNewsSetting("news_show") == "0" {
		return false, nil, nil
	}
	ids := parseSources(getNewsSetting("news_sources"))
	enabled := len(ids) > 0
	if !enabled {
		return false, nil, nil
	}
	order := parseSources(getNewsSetting("news_order"))
	return true, ids, order
}

func (h *NewsHandler) Sources(w http.ResponseWriter, r *http.Request) {
	_, _, order := newsEnabledSetting()
	publics := orderedSources(nil, order)
	out := make([]newsSourcePublic, 0, len(publics))
	for _, s := range publics {
		out = append(out, sourceToPublic(s))
	}
	enabled, _, _ := newsEnabledSetting()
	OK(w, map[string]any{"enabled": enabled, "sources": out})
}

func (h *NewsHandler) Source(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	latest := r.URL.Query().Get("latest") == "1"

	// 自定义 RSS 源：id 形如 feed_<id>，按需查 URL 后走带缓存的抓取
	if strings.HasPrefix(id, "feed_") {
		var feedURL string
		if e := db.DB.QueryRow("SELECT url FROM custom_feeds WHERE enabled=1 AND id=?",
			strings.TrimPrefix(id, "feed_")).Scan(&feedURL); e != nil || feedURL == "" {
			Fail(w, "未知数据源")
			return
		}
		items := fetchCustomFeedCached(id, feedURL, latest)
		updated := time.Now().Unix()
		if _, at, ok := cachePeek(id); ok {
			updated = at.Unix()
		}
		OK(w, map[string]any{"id": id, "status": newsStatus(items), "updated": updated, "items": items})
		return
	}

	var target *newsSourceMeta
	for i := range allNewsSources {
		if allNewsSources[i].ID == id {
			target = &allNewsSources[i]
			break
		}
	}
	if target == nil {
		Fail(w, "未知数据源")
		return
	}
	items := fetchSource(*target, latest)
	updated := time.Now().Unix()
	if _, at, ok := cachePeek(id); ok {
		updated = at.Unix()  // 返回真实抓取时间，前端据此判断是否需要刷新
	}
	OK(w, map[string]any{"id": target.ID, "status": newsStatus(items), "updated": updated, "items": items})
}

func (h *NewsHandler) All(w http.ResponseWriter, r *http.Request) {
	enabled, ids, order := newsEnabledSetting()
	if !enabled {
		OK(w, map[string]any{"enabled": false, "sources": []newsSourcePublic{}, "list": []map[string]any{}})
		return
	}
	// 重启后首次访问：先把落盘快照装回内存，首屏直接有内容（过期由后台刷新）
	loadNewsCacheRows()

	ordered := orderedSources(ids, order)
	publics := make([]newsSourcePublic, 0, len(ordered)+4)
	for _, src := range ordered {
		publics = append(publics, sourceToPublic(src))
	}

	// stale-while-revalidate 的槽位：本次响应只读缓存（含过期快照），
	// 缺失/过期的源在后台并发刷新，前端拿到响应后按"过期源"逐卡补齐
	type slot struct {
		mu      sync.Mutex
		items   []newsItem
		updated int64
		status  string
	}
	idsList := make([]string, 0, len(ordered)+4)
	slots := make([]*slot, 0, len(ordered)+4)
	sem := make(chan struct{}, 8) // 后台并发抓取上限，避免一次性打满上游

	fillFromCache := func(id string, freshFor time.Duration) (*slot, bool) {
		sl := &slot{status: "loading"}
		if items, at, ok := cachePeek(id); ok {
			sl.items = items
			sl.updated = at.Unix()
			sl.status = newsStatus(items)
			if time.Since(at) < freshFor {
				return sl, true // 仍然新鲜，不需要回源
			}
		}
		return sl, false
	}

	for _, src := range ordered {
		sl, fresh := fillFromCache(src.ID, newsTTL(src))
		idsList = append(idsList, src.ID)
		slots = append(slots, sl)
		if fresh {
			continue
		}
		go func(src newsSourceMeta, sl *slot) {
			sem <- struct{}{}
			defer func() { <-sem }()
			items := fetchSource(src, false)
			sl.mu.Lock()
			sl.items = items
			if _, at, ok := cachePeek(src.ID); ok {
				sl.updated = at.Unix()
			}
			sl.status = newsStatus(items)
			sl.mu.Unlock()
		}(src, sl)
	}

	var customFeeds []CustomFeedRow
	feedQ, err := db.DB.Query("SELECT id, title, url, sort, enabled, created_at FROM custom_feeds WHERE enabled=1 ORDER BY sort ASC, id ASC")
	if err == nil {
		for feedQ.Next() {
			var cf CustomFeedRow
			feedQ.Scan(&cf.ID, &cf.Title, &cf.URL, &cf.Sort, &cf.Enabled, &cf.CreatedAt)
			customFeeds = append(customFeeds, cf)
		}
		feedQ.Close()
	}

	for _, cf := range customFeeds {
		srcID := "feed_" + strconv.Itoa(cf.ID)
		publics = append(publics, newsSourcePublic{ID: srcID, Name: cf.Title, Color: "#3498db", Type: "custom_rss", Home: cf.URL, Logo: "", Interval: 600})
		sl, fresh := fillFromCache(srcID, 10*time.Minute)
		idsList = append(idsList, srcID)
		slots = append(slots, sl)
		if fresh {
			continue
		}
		go func(cf CustomFeedRow, srcID string, sl *slot) {
			sem <- struct{}{}
			defer func() { <-sem }()
			items := fetchCustomFeedCached(srcID, cf.URL, false)
			sl.mu.Lock()
			sl.items = items
			if _, at, ok := cachePeek(srcID); ok {
				sl.updated = at.Unix()
			}
			sl.status = newsStatus(items)
			sl.mu.Unlock()
		}(cf, srcID, sl)
	}

	list := make([]map[string]any, 0, len(slots))
	for i, id := range idsList {
		sl := slots[i]
		sl.mu.Lock()
		items := sl.items
		if items == nil {
			items = []newsItem{}
		}
		list = append(list, map[string]any{"id": id, "status": sl.status, "updated": sl.updated, "items": items})
		sl.mu.Unlock()
	}
	OK(w, map[string]any{"enabled": true, "sources": publics, "list": list})
}

func (h *NewsHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if action == "" {
		action = r.FormValue("action")
	}
	if action == "" {
		action = "meta"
	}
	switch action {
	case "all":
		h.All(w, r)
	case "source":
		h.Source(w, r)
	case "meta":
		fallthrough
	default:
		h.Sources(w, r)
	}
}

func fetchZhihu() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.zhihu.com/api/v3/feed/topstory/hot-list-web?limit=20&desktop=true",
		map[string]string{"Referer": "https://www.zhihu.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data []struct {
			Target struct {
				TitleArea struct {
					Text string `json:"text"`
				} `json:"title_area"`
				Link struct {
					URL string `json:"url"`
				} `json:"link"`
				MetricsArea struct {
					Text string `json:"text"`
				} `json:"metrics_area"`
			} `json:"target"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data))
	for i, d := range raw.Data {
		title := strings.TrimSpace(d.Target.TitleArea.Text)
		if title == "" {
			continue
		}
		n := newItem("zh", fmt.Sprintf("%d", i), title, d.Target.Link.URL, strings.TrimSpace(d.Target.MetricsArea.Text))
		result = append(result, n)
	}
	return result
}

func fetchBaidu() []newsItem {
	status, body, err := newsHTTP("GET", "https://top.baidu.com/api/board?platform=pc&tab=realtime",
		map[string]string{"Referer": "https://top.baidu.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			Cards []struct {
				Content []struct {
					Word     string `json:"word"`
					HotScore string `json:"hotScore"`
				} `json:"content"`
			} `json:"cards"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	if len(raw.Data.Cards) == 0 {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.Cards[0].Content))
	for i, c := range raw.Data.Cards[0].Content {
		title := strings.TrimSpace(c.Word)
		if title == "" {
			continue
		}
		u := "https://www.baidu.com/s?wd=" + url.QueryEscape(title)
		n := newItem("bd", fmt.Sprintf("%d", i), title, u, fmt.Sprintf("%s 热度", c.HotScore))
		result = append(result, n)
	}
	return result
}

func fetchBilibili() []newsItem {
	status, body, err := newsHTTP("GET", "https://api.bilibili.com/x/web-interface/popular?ps=20&pn=1",
		map[string]string{"Referer": "https://www.bilibili.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			List []struct {
				Title string `json:"title"`
				Bvid  string `json:"bvid"`
				Stat  struct {
					View int64 `json:"view"`
				} `json:"stat"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.List))
	for i, v := range raw.Data.List {
		title := strings.TrimSpace(v.Title)
		if title == "" {
			continue
		}
		u := "https://www.bilibili.com/video/" + v.Bvid
		n := newItem("bl", fmt.Sprintf("%d", i), title, u, fmt.Sprintf("%d 播放", v.Stat.View))
		result = append(result, n)
	}
	return result
}

func fetchWeibo() []newsItem {
	status, body, err := newsHTTP("GET", "https://weibo.com/ajax/side/hotSearch",
		map[string]string{
			"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
			"Referer":    "https://weibo.com/",
		}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			Realtime []struct {
				Word     string `json:"word"`
				Note     string `json:"note"`
				Num      int64  `json:"num"`
				Category string `json:"category"`
			} `json:"realtime"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.Realtime))
	for i, r := range raw.Data.Realtime {
		title := strings.TrimSpace(r.Word)
		if title == "" {
			continue
		}
		u := "https://s.weibo.com/weibo?q=%23" + url.QueryEscape(title) + "%23"
		info := ""
		if r.Num > 0 {
			hot := float64(r.Num)
			if hot >= 10000 {
				info = fmt.Sprintf("%.1f万热度", hot/10000)
			} else {
				info = fmt.Sprintf("%d热度", r.Num)
			}
		}
		n := newItem("wb", fmt.Sprintf("%d", i), title, u, info)
		result = append(result, n)
	}
	return result
}

func fetchHackerNews() []newsItem {
	status, body, err := newsHTTP("GET", "https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage=20",
		nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Hits []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			ObjectID    string `json:"objectID"`
			Points      int    `json:"points"`
			NumComments int    `json:"num_comments"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Hits))
	for _, h := range raw.Hits {
		title := strings.TrimSpace(h.Title)
		if title == "" {
			continue
		}
		u := h.URL
		if u == "" {
			u = "https://news.ycombinator.com/item?id=" + h.ObjectID
		}
		info := fmt.Sprintf("%d 分 · %d 评论", h.Points, h.NumComments)
		n := newItem("hn", h.ObjectID, title, u, info)
		result = append(result, n)
	}
	return result
}

func fetchGitHub() []newsItem {
	status, body, err := newsHTTP("GET", "https://github.com/trending?since=daily",
		map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	html := string(body)
	articleRe := regexp.MustCompile(`<article class="Box-row">([\s\S]*?)</article>`)
	titleRe := regexp.MustCompile(`<h2 class="h3 lh-condensed">[\s\S]*?<a[^>]*href="(/[^"]+)"`)
	starsRe := regexp.MustCompile(`<a[^>]*href="[^"]*stargazers[^"]*"[^>]*>([\s\S]*?)</a>`)
	result := make([]newsItem, 0, 20)
	articles := articleRe.FindAllStringSubmatch(html, -1)
	for i, art := range articles {
		if len(art) < 2 {
			continue
		}
		m := titleRe.FindStringSubmatch(art[1])
		if len(m) < 2 {
			continue
		}
		href := m[1]
		title := strings.ReplaceAll(href, "/", " ")
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		u := "https://github.com" + href
		info := ""
		sm := starsRe.FindStringSubmatch(art[1])
		if len(sm) >= 2 {
			s := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(sm[1], "")
			s = strings.ReplaceAll(s, "\n", " ")
			s = strings.TrimSpace(s)
			if s != "" {
				info = "★ " + s
			}
		}
		n := newItem("gh", fmt.Sprintf("%d", i), title, u, info)
		result = append(result, n)
	}
	return result
}

func fetchThepaper() []newsItem {
	status, body, err := newsHTTP("GET", "https://cache.thepaper.cn/contentapi/wwwIndex/rightSidebar",
		nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			HotNews []struct {
				Name           string `json:"name"`
				ContID         string `json:"contId"`
				InteractionNum string `json:"interactionNum"`
			} `json:"hotNews"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.HotNews))
	for i, h := range raw.Data.HotNews {
		title := strings.TrimSpace(h.Name)
		if title == "" {
			continue
		}
		u := "https://www.thepaper.cn/newsDetail_forward_" + h.ContID
		info := h.InteractionNum + " 互动"
		n := newItem("tp", fmt.Sprintf("%d", i), title, u, info)
		result = append(result, n)
	}
	return result
}

func fetchToutiao() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.toutiao.com/hot-event/hot-board/?origin=toutiao_pc",
		map[string]string{"Referer": "https://www.toutiao.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data []struct {
			Title        string `json:"Title"`
			ClusterIDStr string `json:"ClusterIdStr"`
			HotValue     string `json:"HotValue"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data))
	for i, d := range raw.Data {
		title := strings.TrimSpace(d.Title)
		if title == "" {
			continue
		}
		u := "https://www.toutiao.com/trending/" + d.ClusterIDStr + "/"
		info := ""
		if d.HotValue != "" {
			info = d.HotValue + " 热度"
		}
		n := newItem("tt", fmt.Sprintf("%d", i), title, u, info)
		result = append(result, n)
	}
	return result
}

func fetchTieba() []newsItem {
	status, body, err := newsHTTP("GET", "https://tieba.baidu.com/hottopic/browse/topicList",
		map[string]string{"Referer": "https://tieba.baidu.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			BangTopic struct {
				TopicList []struct {
					TopicName string `json:"topic_name"`
					TopicURL  string `json:"topic_url"`
				} `json:"topic_list"`
			} `json:"bang_topic"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.BangTopic.TopicList))
	for i, t := range raw.Data.BangTopic.TopicList {
		title := strings.TrimSpace(htmlUnescape(t.TopicName))
		if title == "" {
			continue
		}
		u := htmlUnescape(t.TopicURL)
		if u == "" {
			u = "https://tieba.baidu.com/hottopic/browse/topicList"
		}
		n := newItem("tb", fmt.Sprintf("%d", i), title, u, "")
		result = append(result, n)
	}
	return result
}

func fetchDouban() []newsItem {
	status, body, err := newsHTTP("GET", "https://m.douban.com/rexxar/api/v2/subject/recent_hot/movie",
		map[string]string{
			"Referer": "https://movie.douban.com/",
			"Accept":  "application/json",
		}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Items []struct {
			Title  string `json:"title"`
			ID     string `json:"id"`
			Rating struct {
				Value float64 `json:"value"`
			} `json:"rating"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Items))
	for i, it := range raw.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		u := "https://movie.douban.com/subject/" + it.ID
		info := fmt.Sprintf("\u2605 %.1f", it.Rating.Value)
		n := newItem("db", fmt.Sprintf("%d", i), title, u, info)
		result = append(result, n)
	}
	return result
}

func fetchHupu() []newsItem {
	status, body, err := newsHTTP("GET", "https://bbs.hupu.com/topic-daily-hot",
		nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	re := regexp.MustCompile(`<li class="bbs-sl-web-post-body">[\s\S]*?<a\s+href="(/[^"]+?\.html)"[^>]*class="p-title"[^>]*>([^<]+)</a>`)
	matches := re.FindAllStringSubmatch(string(body), -1)
	result := make([]newsItem, 0, len(matches))
	for i, m := range matches {
		if len(m) < 3 {
			continue
		}
		title := strings.TrimSpace(htmlUnescape(m[2]))
		if title == "" {
			continue
		}
		u := "https://bbs.hupu.com" + m[1]
		n := newItem("hp", fmt.Sprintf("%d", i), title, u, "")
		result = append(result, n)
	}
	return result
}

func fetchJuejin() []newsItem {
	status, body, err := newsHTTP("GET", "https://api.juejin.cn/content_api/v1/content/article_rank?category_id=1&type=hot&spider=0",
		map[string]string{"Referer": "https://juejin.cn/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data []struct {
			Content struct {
				Title     string `json:"title"`
				ContentID string `json:"content_id"`
			} `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data))
	for i, d := range raw.Data {
		title := strings.TrimSpace(d.Content.Title)
		if title == "" {
			continue
		}
		u := "https://juejin.cn/post/" + d.Content.ContentID
		n := newItem("jj", fmt.Sprintf("%d", i), title, u, "")
		result = append(result, n)
	}
	return result
}

func fetchSspai() []newsItem {
	status, body, err := newsHTTP("GET", "https://sspai.com/api/v1/article/tag/page/get?limit=20&offset=0&tag=%E7%83%AD%E9%97%A8%E6%96%87%E7%AB%A0",
		map[string]string{"Referer": "https://sspai.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data []struct {
			Title string `json:"title"`
			ID    int64  `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data))
	for i, d := range raw.Data {
		title := strings.TrimSpace(d.Title)
		if title == "" {
			continue
		}
		u := fmt.Sprintf("https://sspai.com/post/%d", d.ID)
		n := newItem("sp", fmt.Sprintf("%d", i), title, u, "")
		result = append(result, n)
	}
	return result
}

func fetchNowcoder() []newsItem {
	status, body, err := newsHTTP("GET", "https://gw-c.nowcoder.com/api/sparta/hot-search/top-hot-pc?size=20",
		map[string]string{"Referer": "https://www.nowcoder.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			Result []struct {
				Title string `json:"title"`
				Type  int    `json:"type"`
				UUID  string `json:"uuid"`
				ID    string `json:"id"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.Result))
	for i, r := range raw.Data.Result {
		title := strings.TrimSpace(r.Title)
		if title == "" {
			continue
		}
		u := ""
		if r.Type == 74 && r.UUID != "" {
			u = "https://www.nowcoder.com/feed/main/detail/" + r.UUID
		} else if r.Type == 0 && r.ID != "" {
			u = "https://www.nowcoder.com/discuss/" + r.ID
		} else {
			continue
		}
		n := newItem("nc", fmt.Sprintf("%d", i), title, u, "")
		result = append(result, n)
	}
	return result
}

func fetchTencent() []newsItem {
	status, body, err := newsHTTP("GET", "https://i.news.qq.com/web_backend/v2/getTagInfo?tagId=aEWqxLtdgmQ%3D",
		map[string]string{"Referer": "https://news.qq.com/"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			Tabs []struct {
				ArticleList []struct {
					Title    string `json:"title"`
					LinkInfo struct {
						URL string `json:"url"`
					} `json:"link_info"`
				} `json:"articleList"`
			} `json:"tabs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	if len(raw.Data.Tabs) == 0 {
		return nil
	}
	articles := raw.Data.Tabs[0].ArticleList
	result := make([]newsItem, 0, len(articles))
	for i, a := range articles {
		title := strings.TrimSpace(a.Title)
		if title == "" {
			continue
		}
		n := newItem("tx", fmt.Sprintf("%d", i), title, a.LinkInfo.URL, "")
		result = append(result, n)
	}
	return result
}

func fetchProductHunt() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.producthunt.com/feed", nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	items := parseRSS(body)
	if items == nil {
		return nil
	}
	prefixIDs(items, "rs")
	return items
}

func fetchFreeBuf() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.freebuf.com/feed", nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	items := parseRSS(body)
	if items == nil {
		return nil
	}
	prefixIDs(items, "ws")
	return items
}

func fetchITHome() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.ithome.com/rss/", nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	items := parseRSS(body)
	if items == nil {
		return nil
	}
	prefixIDs(items, "it")
	return items
}

func fetchSolidot() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.solidot.org/index.rss", nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	items := parseRSS(body)
	if items == nil {
		return nil
	}
	prefixIDs(items, "so")
	return items
}

func fetchWallstreetcn() []newsItem {
	status, body, err := newsHTTP("GET", "https://api-one-wscn.awtmt.com/apiv1/content/lives?channel=global-channel&limit=20",
		nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			Items []struct {
				Title       string `json:"title"`
				URI         string `json:"uri"`
				DisplayTime int64  `json:"display_time"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.Items))
	for i, it := range raw.Data.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		info := ""
		if it.DisplayTime > 0 {
			info = time.Unix(it.DisplayTime, 0).Format("15:04")
		}
		n := newItem("ws", fmt.Sprintf("%d", i), title, it.URI, info)
		result = append(result, n)
	}
	return result
}

func fetchV2ex() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.v2ex.com/feed/share.json", nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Items []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			DatePublished string `json:"date_published"`
			DateModified  string `json:"date_modified"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Items))
	for i, it := range raw.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		n := newItem("v2", fmt.Sprintf("%d", i), title, it.URL, it.DatePublished)
		result = append(result, n)
	}
	return result
}

func fetchDouyin() []newsItem {
	status, body, err := newsHTTP("GET", "https://www.iesdouyin.com/web/api/v2/hotsearch/billboard/word/",
		nil, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		WordList []struct {
			Word     string `json:"word"`
			HotValue int64  `json:"hot_value"`
		} `json:"word_list"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.WordList))
	for i, w := range raw.WordList {
		title := strings.TrimSpace(w.Word)
		if title == "" {
			continue
		}
		u := "https://www.douyin.com/search/" + url.QueryEscape(title)
		info := fmt.Sprintf("%d 热度", w.HotValue)
		n := newItem("dy", fmt.Sprintf("%d", i), title, u, info)
		result = append(result, n)
	}
	return result
}

func fetch36Kr() []newsItem {
	payload := []byte(`{"partner_id":"web","param":{"siteId":1,"platformId":2}}`)
	status, body, err := newsHTTP("POST", "https://gateway.36kr.com/api/mis/nav/home/nav/rank/hot",
		map[string]string{"Content-Type": "application/json"}, payload, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data struct {
			HotRankList []struct {
				ItemID           int64 `json:"itemId"`
				TemplateMaterial struct {
					WidgetTitle string `json:"widgetTitle"`
					StatPraise  int64  `json:"statPraise"`
				} `json:"templateMaterial"`
			} `json:"hotRankList"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data.HotRankList))
	for i, h := range raw.Data.HotRankList {
		title := strings.TrimSpace(h.TemplateMaterial.WidgetTitle)
		if title == "" {
			continue
		}
		u := fmt.Sprintf("https://36kr.com/p/%d", h.ItemID)
		info := fmt.Sprintf("%d 点赞", h.TemplateMaterial.StatPraise)
		n := newItem("kr", fmt.Sprintf("%d", i), title, u, info)
		result = append(result, n)
	}
	return result
}

func fetchCSDN() []newsItem {
	status, body, err := newsHTTP("GET", "https://blog.csdn.net/phoenix/web/blog/hot-rank?page=0&pageSize=25",
		map[string]string{"Referer": "https://blog.csdn.net/rank/list"}, nil, 10*time.Second)
	if err != nil || status != 200 {
		return nil
	}
	var raw struct {
		Data []struct {
			ArticleTitle     string `json:"articleTitle"`
			ArticleDetailURL string `json:"articleDetailUrl"`
			HotRankScore     string `json:"hotRankScore"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	result := make([]newsItem, 0, len(raw.Data))
	for i, d := range raw.Data {
		title := strings.TrimSpace(d.ArticleTitle)
		if title == "" {
			continue
		}
		n := newItem("cs", fmt.Sprintf("%d", i), title, d.ArticleDetailURL, d.HotRankScore+" 热度")
		result = append(result, n)
	}
	return result
}
