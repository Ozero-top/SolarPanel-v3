package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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

	today := time.Now().Format("2006-01-02")
	cacheFile := filepath.Join(cacheDir, fmt.Sprintf("%s_%s.json", source, today))
	imageFile := filepath.Join(cacheDir, fmt.Sprintf("%s_%s.jpg", source, today))

	if data, err := os.ReadFile(cacheFile); err == nil {
		var meta map[string]any
		if json.Unmarshal(data, &meta) == nil {
			if _, err := os.Stat(imageFile); err == nil {
				meta["cache"] = "hit"
				meta["url"] = h.buildImageURL(source, today)
				OK(w, meta)
				go h.cleanupOldCache(cacheDir)
				return
			}
		}
	}

	if fallback := h.findLatestCache(cacheDir, source); fallback != "" {
		if data, err := os.ReadFile(fallback + ".json"); err == nil {
			var meta map[string]any
			if json.Unmarshal(data, &meta) == nil {
				meta["cache"] = "fallback"
				re := regexp.MustCompile(fmt.Sprintf(`%s_(\d{4}-\d{2}-\d{2})\.json`, regexp.QuoteMeta(source)))
				m := re.FindStringSubmatch(fallback)
				date := today
				if len(m) > 1 {
					date = m[1]
				}
				meta["url"] = h.buildImageURL(source, date)
				OK(w, meta)
				go h.cleanupOldCache(cacheDir)
				return
			}
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	var (
		meta map[string]any
		err  error
	)
	switch source {
	case "nasa":
		meta, err = h.fetchNasa(client)
	case "picsum":
		meta, err = h.fetchPicsum(client, today)
	default:
		meta, err = h.fetchBing(client, "zh-CN", source)
	}
	if err != nil {
		if anyCache := h.findAnyLatestCache(cacheDir); anyCache != "" {
			if data, err := os.ReadFile(anyCache + ".json"); err == nil {
				var m map[string]any
				if json.Unmarshal(data, &m) == nil {
					m["cache"] = "stale"
					re := regexp.MustCompile(`(\w+)_(\d{4}-\d{2}-\d{2})\.json`)
					m2 := re.FindStringSubmatch(anyCache)
					if len(m2) > 2 {
						m["url"] = h.buildImageURL(m2[1], m2[2])
					}
					OK(w, m)
					go h.cleanupOldCache(cacheDir)
					return
				}
			}
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
	bin, err := h.downloadImage(client, src)
	if err != nil {
		return nil, fmt.Errorf("Bing 图片下载失败: %w", err)
	}
	return map[string]any{"title": img.Title, "copyright": img.Copyright, "_image": bin}, nil
}

func (h *WallpaperHandler) fetchNasa(client *http.Client) (map[string]any, error) {
	apiKey := os.Getenv("NASA_API_KEY")
	if apiKey == "" {
		apiKey = "DEMO_KEY"
	}
	api := fmt.Sprintf("https://api.nasa.gov/planetary/apod?api_key=%s&hd=true", apiKey)
	resp, err := client.Get(api)
	if err != nil {
		return nil, fmt.Errorf("NASA API 失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("NASA 响应读取失败: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("NASA 返回格式异常")
	}
	url, _ := payload["hdurl"].(string)
	if url == "" {
		url, _ = payload["url"].(string)
	}
	if url == "" {
		return nil, fmt.Errorf("NASA 无图片 URL")
	}
	mediaType, _ := payload["media_type"].(string)
	if mediaType == "video" {
		return nil, fmt.Errorf("NASA 今日是视频，不支持作为壁纸")
	}
	bin, err := h.downloadImage(client, url)
	if err != nil {
		return nil, fmt.Errorf("NASA 图片下载失败: %w", err)
	}
	title, _ := payload["title"].(string)
	explanation, _ := payload["explanation"].(string)
	if len(explanation) > 200 {
		explanation = explanation[:200] + "…"
	}
	copyright, _ := payload["copyright"].(string)
	return map[string]any{"title": title, "copyright": copyright, "explanation": explanation, "media_type": mediaType, "_image": bin}, nil
}

func (h *WallpaperHandler) fetchPicsum(client *http.Client, date string) (map[string]any, error) {
	src := fmt.Sprintf("https://picsum.photos/seed/sp-%s/1920/1080", date)
	bin, err := h.downloadImage(client, src)
	if err != nil {
		return nil, fmt.Errorf("Picsum 图片下载失败: %w", err)
	}
	return map[string]any{"title": fmt.Sprintf("Lorem Picsum · %s", date), "copyright": "loremflickr.com / picsum.photos", "_image": bin}, nil
}

func (h *WallpaperHandler) downloadImage(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bin, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(bin) < 2048 {
		return nil, fmt.Errorf("图片过小（%d bytes）", len(bin))
	}
	return bin, nil
}

func (h *WallpaperHandler) findLatestCache(cacheDir, source string) string {
	entries, _ := filepath.Glob(filepath.Join(cacheDir, fmt.Sprintf("%s_*.json", source)))
	if len(entries) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(entries)))
	for _, e := range entries {
		imgPath := e[:len(e)-5] + ".jpg"
		if _, err := os.Stat(imgPath); err == nil {
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
		if _, err := os.Stat(imgPath); err == nil {
			return e[:len(e)-5]
		}
	}
	return ""
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
	return fmt.Sprintf("/uploads/daily_wallpaper/%s_%s.jpg", source, date)
}

func normalizeWallpaperSource(raw string) string {
	switch raw {
	case "", "bing", "bing_en", "bing_jp":
		return "bing_cn"
	case "bing_cn", "nasa", "picsum":
		return raw
	default:
		return "bing_cn"
	}
}

// ===== News 的 fetch 函数都定义在 news_handler.go =====

// 这些 fetch 函数直接内联在 news_handler.go，以下只是补充 weather 包
// newsSourcePublic, newsSourceListEntry, newsMetaResponse, newsAllResponse, newsSourceResponse
// 这些类型在 feeds_handler.go 里定义了

var _ = strings.TrimSpace
