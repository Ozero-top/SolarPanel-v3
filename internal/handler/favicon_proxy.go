package handler

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/config"
)

type FaviconProxyHandler struct {
	cfg *config.Config
}

func NewFaviconProxyHandler(cfg *config.Config) *FaviconProxyHandler {
	return &FaviconProxyHandler{cfg: cfg}
}

var faviconHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

var faviconProxyContentTypes = map[string]string{
	".png": "image/png", ".ico": "image/x-icon", ".jpg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml",
}

type faviconFetchSlot struct {
	done chan struct{}
	path string
}

var faviconInflight sync.Map

// 失败冷却：24h 内不再重复抓取，避免每次刷新都卡在坏站点上
var faviconNegative sync.Map
var faviconNegativeTTL = 24 * time.Hour

func faviconCachePath(cfg *config.Config, host string) (diskPath, urlPath, ext string) {
	md5h := md5Hex(host)
	dir := filepath.Join(cfg.UploadDir, "favicon")
	for _, e := range faviconExts {
		p := filepath.Join(dir, md5h+e)
		_, err := os.Stat(p)
		if err != nil {
			continue
		}
		return p, "/frontend/uploads/favicon/" + md5h + e, e
	}
	return "", "", ""
}

func (h *FaviconProxyHandler) Proxy(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("host")))
	if host == "" || len(host) > 253 || !faviconHostRe.MatchString(host) || strings.Contains(host, "..") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if isPrivateHost(host) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// ?refresh=1 允许后台手动重建图标：清本地文件与失败冷却后重新抓取
	if r.URL.Query().Get("refresh") == "1" {
		if disk, _, _ := faviconCachePath(h.cfg, host); disk != "" {
			os.Remove(disk)
		}
		faviconNegative.Delete(host)
	}

	if disk, _, ext := faviconCachePath(h.cfg, host); disk != "" {
		h.serveFile(w, r, disk, ext)
		return
	}
	if v, ok := faviconNegative.Load(host); ok {
		if t, _ := v.(time.Time); time.Since(t) < faviconNegativeTTL {
			h.serveNotFound(w)
			return
		}
		faviconNegative.Delete(host)
	}

	slot := &faviconFetchSlot{done: make(chan struct{})}
	actual, loaded := faviconInflight.LoadOrStore(host, slot)
	if loaded {
		wait := actual.(*faviconFetchSlot)
		<-wait.done
		if disk, _, e := faviconCachePath(h.cfg, host); disk != "" {
			h.serveFile(w, r, disk, e)
			return
		}
		if wait.path != "" {
			h.serveURLPath(w, r, wait.path)
			return
		}
		h.serveNotFound(w)
		return
	}

	// 走完整解析：HTML 里的 icon / manifest / 常见默认路径 → 公共源兜底
	slot.path = trySaveIcon(h.cfg, "https://"+host)
	close(slot.done)
	faviconInflight.Delete(host)

	if disk, _, e := faviconCachePath(h.cfg, host); disk != "" {
		h.serveFile(w, r, disk, e)
		return
	}
	// 目标站点发生跳转时图标会按最终域名落盘，这里回退到实际保存的文件
	if slot.path != "" {
		h.serveURLPath(w, r, slot.path)
		return
	}
	// 不再伪装成 200：让前端 onerror 继续回退到下一个源，最后用文字图标
	faviconNegative.Store(host, time.Now())
	log.Printf("[favicon] host=%s — all candidates failed, served 404", host)
	h.serveNotFound(w)
}

// serveNotFound 图标不存在时返回 404（带短缓存，避免同一次访问中反复请求）
func (h *FaviconProxyHandler) serveNotFound(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusNotFound)
}

// serveURLPath 按落盘 URL（/frontend/uploads/...）直接取文件返回
func (h *FaviconProxyHandler) serveURLPath(w http.ResponseWriter, r *http.Request, urlPath string) {
	rel := strings.TrimPrefix(urlPath, "/frontend/uploads/")
	if rel == urlPath || rel == "" {
		h.serveNotFound(w)
		return
	}
	disk := filepath.Join(h.cfg.UploadDir, filepath.FromSlash(rel))
	h.serveFile(w, r, disk, strings.ToLower(filepath.Ext(disk)))
}

func (h *FaviconProxyHandler) serveFile(w http.ResponseWriter, r *http.Request, diskPath, ext string) {
	st, err := os.Stat(diskPath)
	if err != nil || st.Size() == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// ETag = size + mtime unix → 文件未变时稳定
	etag := `"` + strconv.FormatInt(st.Size(), 10) + `-` + strconv.FormatInt(st.ModTime().Unix(), 10) + `"`

	// 协商缓存：浏览器发 If-None-Match → 匹配直接 304，零响应体
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		for _, tag := range strings.Split(inm, ",") {
			if strings.TrimSpace(tag) == etag {
				w.Header().Set("ETag", etag)
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	// 有新内容：读文件返回 200
	data, err := os.ReadFile(diskPath)
	if err != nil || len(data) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	ct := faviconProxyContentTypes[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Last-Modified", st.ModTime().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
