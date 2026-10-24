package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
)

type WallpaperHandler struct {
	cfg *config.Config
}

func NewWallpaperHandler(cfg *config.Config) *WallpaperHandler {
	return &WallpaperHandler{cfg: cfg}
}

type bingImageArchive struct {
	Images []struct {
		URLBase   string `json:"urlbase"`
		Title     string `json:"title"`
		Copyright string `json:"copyright"`
		StartDate string `json:"startdate"`
	} `json:"images"`
}

func (h *WallpaperHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if action == "" {
		action = r.FormValue("action")
	}
	switch action {
	case "daily", "":
		h.Daily(w, r)
	case "preview":
		h.Preview(w, r)
	case "pin":
		h.Pin(w, r)
	case "unpin":
		h.Unpin(w, r)
	default:
		Err(w, http.StatusBadRequest, 1, "unknown action: "+action)
	}
}

func (h *WallpaperHandler) Daily(w http.ResponseWriter, r *http.Request) {
	rawSource := r.URL.Query().Get("source")
	if rawSource == "" {
		rawSource = r.FormValue("source")
	}
	source := normalizeWallpaperSource(rawSource)

	cacheDir := filepath.Join(h.cfg.UploadDir, "daily_wallpaper")
	os.MkdirAll(cacheDir, 0755)

	// 后台「预览弹窗」里选定的固定壁纸优先，取消固定后恢复每日自动
	if meta, ok := h.readPinned(cacheDir, source); ok {
		meta["cache"] = "pinned"
		OK(w, meta)
		return
	}

	today := time.Now().Format("2006-01-02")
	cacheFile, err := safeJoin(cacheDir, source+"_"+today+".json")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "路径错误")
		return
	}
	imageFile, err := safeJoin(cacheDir, source+"_"+today+".jpg")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "路径错误")
		return
	}

	if meta, ok := h.readDailyCache(cacheDir, source, today); ok {
		meta["cache"] = "hit"
		OK(w, meta)
		go h.cleanupOldCache(cacheDir)
		return
	}
	// 缓存不可用（占位图 / 分辨率过低 / 竖图）→ 清掉重新抓
	os.Remove(cacheFile)
	os.Remove(imageFile)

	client := &http.Client{Timeout: 12 * time.Second}

	// 并发合并：同一源 + 同一天只允许一个请求真正下载，其余等待后直接读缓存，
	// 避免多人同时打开主页时重复抓取上游图片。
	slot := &wallpaperSlot{done: make(chan struct{})}
	inflightKey := source + "_" + today
	if actual, loaded := wallpaperInflight.LoadOrStore(inflightKey, slot); loaded {
		<-actual.(*wallpaperSlot).done
		if meta, ok := h.readDailyCache(cacheDir, source, today); ok {
			meta["cache"] = "hit"
			OK(w, meta)
			return
		}
		if meta, ok := h.readAnyLatestCache(cacheDir); ok {
			OK(w, meta)
			return
		}
		Err(w, http.StatusBadGateway, 1, "每日壁纸获取失败，请稍后重试")
		return
	}
	defer func() {
		close(slot.done)
		wallpaperInflight.Delete(inflightKey)
	}()

	meta, err := h.fetchDailyWithRetry(client, source, today)
	if err != nil {
		// 跨源兜底：Bing 失败换 Picsum，Picsum 失败换 Bing
		alt := "picsum"
		if source == "picsum" {
			alt = "bing_cn"
		}
		if m, e2 := h.fetchDailyWithRetry(client, alt, today); e2 == nil {
			m["fallback_from"] = source
			m["fallback_reason"] = err.Error()
			meta, err = m, nil
		}
	}
	if err != nil {
		// 今日抓取失败：优先用同源历史缓存，其次任意源历史缓存
		if m, ok := h.readLatestCache(cacheDir, source); ok {
			OK(w, m)
			return
		}
		if m, ok := h.readAnyLatestCache(cacheDir); ok {
			OK(w, m)
			return
		}
		Err(w, http.StatusBadGateway, 1, err.Error())
		return
	}

	bin, _ := meta["_image"].([]byte)
	delete(meta, "_image")
	if err := os.WriteFile(imageFile, bin, 0644); err != nil {
		Err(w, http.StatusInternalServerError, 1, "保存壁纸失败")
		return
	}
	meta["source"] = source
	meta["date"] = today
	meta["cache"] = "miss"
	meta["url"] = h.buildImageURL(source, today)
	metaBytes, _ := json.Marshal(meta)
	os.WriteFile(cacheFile, metaBytes, 0644)
	go h.cleanupOldCache(cacheDir)
	OK(w, meta)
}

// wpPreviewItem 预览候选（缩略图走远程 URL，服务端不落盘）
type wpPreviewItem struct {
	ID        string `json:"id"`
	Thumb     string `json:"thumb"`
	Full      string `json:"full"`
	Title     string `json:"title"`
	Copyright string `json:"copyright"`
	Date      string `json:"date"`
}

// Preview 随机返回某壁纸源的候选缩略图，仅请求元数据，不下载原图
func (h *WallpaperHandler) Preview(w http.ResponseWriter, r *http.Request) {
	source := normalizeWallpaperSource(r.URL.Query().Get("source"))
	n := 10
	if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil && v > 0 && v <= 20 {
		n = v
	}
	client := &http.Client{Timeout: 10 * time.Second}
	items, err := h.fetchPreview(client, source, n)
	if err != nil {
		Err(w, http.StatusBadGateway, 1, err.Error())
		return
	}
	cacheDir := filepath.Join(h.cfg.UploadDir, "daily_wallpaper")
	_, pinned := h.readPinned(cacheDir, source)
	OK(w, map[string]any{"source": source, "pinned": pinned, "list": items})
}

func (h *WallpaperHandler) fetchPreview(client *http.Client, source string, n int) ([]wpPreviewItem, error) {
	if source == "picsum" {
		return h.previewPicsum(client, n)
	}
	return h.previewBing(client, n)
}

// previewBing 合并最近两段档案（idx=0 / idx=4）得到 12 张候选，去重后随机取 n 张
func (h *WallpaperHandler) previewBing(client *http.Client, n int) ([]wpPreviewItem, error) {
	var out []wpPreviewItem
	seen := make(map[string]bool)
	for _, idx := range []int{0, 4} {
		api := fmt.Sprintf("https://cn.bing.com/HPImageArchive.aspx?format=js&idx=%d&n=8&mkt=zh-CN", idx)
		resp, err := client.Get(api)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			continue
		}
		var archive bingImageArchive
		if json.Unmarshal(body, &archive) != nil {
			continue
		}
		for _, img := range archive.Images {
			if img.URLBase == "" || seen[img.URLBase] {
				continue
			}
			seen[img.URLBase] = true
			out = append(out, wpPreviewItem{
				ID:        img.URLBase,
				Thumb:     "https://cn.bing.com" + img.URLBase + "_400x240.jpg",
				Full:      "https://cn.bing.com" + img.URLBase + "_1920x1080.jpg",
				Title:     img.Title,
				Copyright: img.Copyright,
				Date:      img.StartDate,
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Bing 预览获取失败")
	}
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// previewPicsum 随机页取图，缩略图用 picsum 的按尺寸接口
func (h *WallpaperHandler) previewPicsum(client *http.Client, n int) ([]wpPreviewItem, error) {
	page := 1 + rand.Intn(60)
	api := fmt.Sprintf("https://picsum.photos/v2/list?page=%d&limit=%d", page, n)
	resp, err := client.Get(api)
	if err != nil {
		return nil, fmt.Errorf("Picsum 预览失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("Picsum 响应读取失败")
	}
	var list []struct {
		ID     string `json:"id"`
		Author string `json:"author"`
	}
	if json.Unmarshal(body, &list) != nil || len(list) == 0 {
		return nil, fmt.Errorf("Picsum 返回格式异常")
	}
	rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	out := make([]wpPreviewItem, 0, len(list))
	for _, it := range list {
		out = append(out, wpPreviewItem{
			ID:        it.ID,
			Thumb:     fmt.Sprintf("https://picsum.photos/id/%s/480/270", it.ID),
			Full:      fmt.Sprintf("https://picsum.photos/id/%s/1920/1080", it.ID),
			Title:     fmt.Sprintf("Picsum #%s · %s", it.ID, it.Author),
			Copyright: it.Author,
		})
	}
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

type pinWallpaperReq struct {
	Source    string `json:"source"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Copyright string `json:"copyright"`
}

// Pin 选中某张预览图：此时才下载原图并固定为该源的壁纸
func (h *WallpaperHandler) Pin(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 403, "无操作权限")
		return
	}
	var req pinWallpaperReq
	if err := BindJSON(r, &req); err != nil {
		Fail(w, "参数错误")
		return
	}
	if req.Source != "bing_cn" && req.Source != "picsum" {
		Fail(w, "不支持的壁纸源")
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		Fail(w, "壁纸地址无效")
		return
	}
	host := strings.ToLower(u.Hostname())
	allowed := host == "bing.com" || strings.HasSuffix(host, ".bing.com") ||
		host == "picsum.photos" || strings.HasSuffix(host, ".picsum.photos")
	if !allowed {
		Fail(w, "仅支持来自预览源的图片地址")
		return
	}

	client := &http.Client{Timeout: 25 * time.Second}
	bin, err := h.downloadImage(client, req.URL, allowedPinHosts)
	if err != nil {
		Fail(w, "图片下载失败："+err.Error())
		return
	}
	if !isUsableWallpaper(bin) {
		Fail(w, "该图片分辨率过低或不是横图，无法作为壁纸")
		return
	}

	cacheDir := filepath.Join(h.cfg.UploadDir, "daily_wallpaper")
	os.MkdirAll(cacheDir, 0755)
	pinnedJPG, err := safeJoin(cacheDir, "pinned_"+req.Source+".jpg")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "路径错误")
		return
	}
	if err := os.WriteFile(pinnedJPG, bin, 0644); err != nil {
		Err(w, http.StatusInternalServerError, 1, "保存壁纸失败")
		return
	}
	meta := map[string]any{
		"source": req.Source, "title": req.Title, "copyright": req.Copyright,
		"pinned": true, "full": req.URL,
		"pinned_at": time.Now().Format("2006-01-02 15:04:05"),
	}
	if b, err := json.Marshal(meta); err == nil {
		pinnedJSON, _ := safeJoin(cacheDir, "pinned_"+req.Source+".json")
		if pinnedJSON != "" {
			os.WriteFile(pinnedJSON, b, 0644)
		}
	}
	OK(w, map[string]any{
		"source": req.Source,
		"url":    "/frontend/uploads/daily_wallpaper/pinned_" + req.Source + ".jpg",
	})
}

type unpinWallpaperReq struct {
	Source string `json:"source"`
}

// Unpin 取消固定，恢复每日自动随机
func (h *WallpaperHandler) Unpin(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 403, "无操作权限")
		return
	}
	var req unpinWallpaperReq
	if err := BindJSON(r, &req); err != nil || (req.Source != "bing_cn" && req.Source != "picsum") {
		Fail(w, "参数错误")
		return
	}
	cacheDir := filepath.Join(h.cfg.UploadDir, "daily_wallpaper")
	pinnedJPG, _ := safeJoin(cacheDir, "pinned_"+req.Source+".jpg")
	pinnedJSON, _ := safeJoin(cacheDir, "pinned_"+req.Source+".json")
	if pinnedJPG != "" {
		os.Remove(pinnedJPG)
	}
	if pinnedJSON != "" {
		os.Remove(pinnedJSON)
	}
	OK(w, nil)
}

func (h *WallpaperHandler) readPinned(cacheDir, source string) (map[string]any, bool) {
	if !safePathComponent(source) {
		return nil, false
	}
	jsonPath, err := safeJoin(cacheDir, "pinned_"+source+".json")
	if err != nil {
		return nil, false
	}
	imgPath, err := safeJoin(cacheDir, "pinned_"+source+".jpg")
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, false
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil || !isUsableWallpaperFile(cacheDir, imgPath) {
		return nil, false
	}
	meta["url"] = "/frontend/uploads/daily_wallpaper/pinned_" + source + ".jpg"
	return meta, true
}

// wallpaperSlot 每日壁纸并发合并槽位
type wallpaperSlot struct {
	done chan struct{}
}

var wallpaperInflight sync.Map

// fetchDailyWithRetry 抓取当日壁纸：最多尝试 2 次，并校验分辨率/体积/横竖比
func (h *WallpaperHandler) fetchDailyWithRetry(client *http.Client, source, today string) (map[string]any, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		var (
			meta map[string]any
			err  error
		)
		if source == "picsum" {
			meta, err = h.fetchPicsum(client, today)
		} else {
			meta, err = h.fetchBing(client, "zh-CN", source)
		}
		if err == nil {
			if bin, ok := meta["_image"].([]byte); ok && isUsableWallpaper(bin) {
				return meta, nil
			}
			err = fmt.Errorf("图片不适合作为壁纸（分辨率过低或为占位图）")
		}
		lastErr = err
		if attempt == 0 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	return nil, lastErr
}

func (h *WallpaperHandler) readDailyCache(cacheDir, source, today string) (map[string]any, bool) {
	if !safePathComponent(source) {
		return nil, false
	}
	cacheFile, err := safeJoin(cacheDir, source+"_"+today+".json")
	if err != nil {
		return nil, false
	}
	imageFile, err := safeJoin(cacheDir, source+"_"+today+".jpg")
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, false
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil || !isUsableWallpaperFile(cacheDir, imageFile) {
		return nil, false
	}
	meta["url"] = h.buildImageURL(source, today)
	return meta, true
}

// readLatestCache 读取同一源最近一次可用缓存（昨天/前天的图）
func (h *WallpaperHandler) readLatestCache(cacheDir, source string) (map[string]any, bool) {
	if !safePathComponent(source) {
		return nil, false
	}
	base := h.findLatestCache(cacheDir, source)
	if base == "" {
		return nil, false
	}
	jsonPath := filepath.Clean(base + ".json")
	if !strings.HasPrefix(jsonPath, filepath.Clean(cacheDir)) {
		return nil, false
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, false
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil {
		return nil, false
	}
	date := ""
	if i := strings.LastIndex(base, "_"); i >= 0 {
		date = base[i+1:]
	}
	meta["cache"] = "fallback"
	meta["url"] = h.buildImageURL(source, date)
	return meta, true
}

// readAnyLatestCache 任意源最近一次可用缓存（跨源兜底）
func (h *WallpaperHandler) readAnyLatestCache(cacheDir string) (map[string]any, bool) {
	base := h.findAnyLatestCache(cacheDir)
	if base == "" {
		return nil, false
	}
	jsonPath := filepath.Clean(base + ".json")
	if !strings.HasPrefix(jsonPath, filepath.Clean(cacheDir)) {
		return nil, false
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, false
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil {
		return nil, false
	}
	re := regexp.MustCompile(`([a-z_]+)_(\d{4}-\d{2}-\d{2})$`)
	m := re.FindStringSubmatch(base)
	if len(m) < 3 {
		return nil, false
	}
	meta["cache"] = "stale"
	meta["url"] = h.buildImageURL(m[1], m[2])
	return meta, true
}

func (h *WallpaperHandler) fetchBing(client *http.Client, mkt, source string) (map[string]any, error) {
	api := fmt.Sprintf("https://cn.bing.com/HPImageArchive.aspx?format=js&idx=0&n=1&mkt=%s", mkt)
	resp, err := client.Get(api)
	if err != nil {
		return nil, fmt.Errorf("Bing API 失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Bing 响应读取失败: %w", err)
	}
	var archive bingImageArchive
	if err := json.Unmarshal(body, &archive); err != nil || len(archive.Images) == 0 {
		return nil, fmt.Errorf("Bing 返回格式异常")
	}
	img := archive.Images[0]
	if img.URLBase == "" {
		return nil, fmt.Errorf("Bing 无图片")
	}
	src := "https://cn.bing.com" + img.URLBase + "_1920x1080.jpg"
	bin, err := h.downloadImage(client, src, nil)
	if err != nil {
		return nil, fmt.Errorf("Bing 图片下载失败: %w", err)
	}
	return map[string]any{"title": img.Title, "copyright": img.Copyright, "_image": bin}, nil
}

func (h *WallpaperHandler) fetchPicsum(client *http.Client, date string) (map[string]any, error) {
	src := fmt.Sprintf("https://picsum.photos/seed/sp-%s/1920/1080", date)
	bin, err := h.downloadImage(client, src, nil)
	if err != nil {
		return nil, fmt.Errorf("Picsum 图片下载失败: %w", err)
	}
	return map[string]any{"title": fmt.Sprintf("Lorem Picsum · %s", date), "copyright": "loremflickr.com / picsum.photos", "_image": bin}, nil
}

var allowedPinHosts = map[string]bool{
	"bing.com":       true,
	"picsum.photos":  true,
}

// isAllowedPinHost 检查 host 是否在允许的壁纸源列表中
func isAllowedPinHost(host string) bool {
	host = strings.ToLower(host)
	if allowedPinHosts[host] {
		return true
	}
	for allowed := range allowedPinHosts {
		if strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func (h *WallpaperHandler) downloadImage(client *http.Client, rawURL string, hostAllowlist map[string]bool) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	if hostAllowlist != nil && !isAllowedPinHost(u.Hostname()) {
		return nil, fmt.Errorf("host not allowed: %s", u.Hostname())
	}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// 限制单张壁纸最大 20MB，避免上游异常响应耗尽内存
	bin, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20+1))
	if err != nil {
		return nil, err
	}
	if len(bin) > 20<<20 {
		return nil, fmt.Errorf("图片过大（超过 20MB）")
	}
	if len(bin) < 2048 {
		return nil, fmt.Errorf("图片过小（%d bytes）", len(bin))
	}
	return bin, nil
}

func (h *WallpaperHandler) findLatestCache(cacheDir, source string) string {
	if !safePathComponent(source) {
		return ""
	}
	entries, _ := filepath.Glob(filepath.Join(cacheDir, source+"_*.json"))
	if len(entries) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))
	for _, e := range entries {
		imgPath := e[:len(e)-5] + ".jpg"
		if isUsableWallpaperFile(cacheDir, imgPath) {
			return e[:len(e)-5]
		}
	}
	return ""
}

func (h *WallpaperHandler) findAnyLatestCache(cacheDir string) string {
	entries, _ := filepath.Glob(filepath.Join(cacheDir, "*_*.json"))
	if len(entries) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))
	for _, e := range entries {
		imgPath := e[:len(e)-5] + ".jpg"
		if isUsableWallpaperFile(cacheDir, imgPath) {
			return e[:len(e)-5]
		}
	}
	return ""
}

// isUsableWallpaper 过滤占位图/图标/竖图：体积、分辨率、横竖比三重校验
func isUsableWallpaper(bin []byte) bool {
	if len(bin) < 60<<10 { // 小于 60KB 基本是 logo / 占位图
		return false
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(bin))
	if err != nil {
		return false
	}
	return cfg.Width >= 1280 && cfg.Height >= 720 && cfg.Width > cfg.Height
}

// isUsableWallpaperFile 过滤占位图/图标/竖图：体积、分辨率、横竖比三重校验
// baseDir 用于确保 path 不逃逸预期目录（防御 path traversal）
func isUsableWallpaperFile(baseDir, path string) bool {
	cleaned := filepath.Clean(path)
	if !strings.HasPrefix(cleaned, filepath.Clean(baseDir)) {
		return false
	}
	st, err := os.Stat(cleaned)
	if err != nil || st.Size() < 60<<10 {
		return false
	}
	f, err := os.Open(cleaned)
	if err != nil {
		return false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return false
	}
	return cfg.Width >= 1280 && cfg.Height >= 720 && cfg.Width > cfg.Height
}

func (h *WallpaperHandler) cleanupOldCache(cacheDir string) {
	cutoff := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	entries, _ := os.ReadDir(cacheDir)
	re := regexp.MustCompile(`^(\w+)_(\d{4}-\d{2}-\d{2})\.(json|jpg)$`)
	for _, e := range entries {
		m := re.FindStringSubmatch(e.Name())
		if len(m) < 4 {
			continue
		}
		if m[2] < cutoff {
			os.Remove(filepath.Join(cacheDir, e.Name()))
		}
	}
}

func (h *WallpaperHandler) buildImageURL(source, date string) string {
	return fmt.Sprintf("/frontend/uploads/daily_wallpaper/%s_%s.jpg", source, date)
}

// normalizeWallpaperSource 强制壁纸源为安全白名单值
func normalizeWallpaperSource(raw string) string {
	switch raw {
	case "", "bing", "bing_en", "bing_jp":
		return "bing_cn"
	case "bing_cn":
		return "bing_cn"
	case "picsum":
		return "picsum"
	default:
		return "bing_cn"
	}
}

// safePathComponent 校验单个路径组件不含分隔符、.. 或空值
// 仅适合作为安全文件名/子目录名的 allowlist 校验
func safePathComponent(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	return true
}

// safeJoin 在 baseDir 内拼接文件名，并验证结果未逃逸 baseDir
func safeJoin(baseDir string, components ...string) (string, error) {
	for _, c := range components {
		if !safePathComponent(c) {
			return "", fmt.Errorf("unsafe path component: %s", c)
		}
	}
	joined := filepath.Join(append([]string{baseDir}, components...)...)
	cleaned := filepath.Clean(joined)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absJoined, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	absBase = strings.TrimRight(absBase, `\/`)
	if !strings.HasPrefix(absJoined, absBase+string(filepath.Separator)) && absJoined != absBase {
		return "", fmt.Errorf("path escapes base directory")
	}
	return cleaned, nil
}
