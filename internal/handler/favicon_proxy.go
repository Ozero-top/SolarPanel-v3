package handler

import (
	"encoding/base64"
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

// faviconDataCache 缓存 host → 已编码的 data URL，批量接口命中后不再逐图标 stat/读盘。
// 写入、手动刷新、启动迁移会显式失效；图标本身极少变动。
type faviconDataEntry struct {
	url  string
	size int64
	mod  int64
}

var faviconDataCache sync.Map

const faviconDataMaxBytes = 64 << 10

func faviconDataURL(cfg *config.Config, host string) (faviconDataEntry, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if v, ok := faviconDataCache.Load(host); ok {
		return v.(faviconDataEntry), true
	}
	disk, _, ext := faviconCachePath(cfg, host)
	if disk == "" {
		return faviconDataEntry{}, false
	}
	st, err := os.Stat(disk)
	if err != nil || st.Size() == 0 || st.Size() > faviconDataMaxBytes {
		return faviconDataEntry{}, false
	}
	ct := faviconProxyContentTypes[ext]
	if ct == "" {
		return faviconDataEntry{}, false
	}
	data, err := os.ReadFile(disk)
	if err != nil || len(data) == 0 {
		return faviconDataEntry{}, false
	}
	e := faviconDataEntry{
		url:  "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data),
		size: st.Size(),
		mod:  st.ModTime().Unix(),
	}
	faviconDataCache.Store(host, e)
	return e, true
}

func invalidateFaviconDataURL(host string) {
	faviconDataCache.Delete(strings.ToLower(strings.TrimSpace(host)))
}

func clearFaviconDataCache() {
	faviconDataCache.Range(func(k, _ any) bool {
		faviconDataCache.Delete(k)
		return true
	})
}

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
	// 批量模式：?hosts=a,b,c 一次返回多个域名已缓存的图标（data URL）。
	// 首屏可见卡片往往几十张，逐卡请求在弱网上行下要排好几轮，合并成一次请求快得多。
	if r.URL.Query().Get("hosts") != "" {
		h.batchIcons(w, r)
		return
	}
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
		invalidateFaviconDataURL(host)
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

const (
	batchIconMaxHosts = 48
	// 图标转 WebP 后中位数约 1.5KB，48 张约 72KB；这里再用总量兜底，
	// 避免个别超大 SVG 把一次响应撑到几百 KB
	batchIconMaxBytes = 192 << 10
)

// batchIcons 批量返回多个域名已缓存的图标：只读本地磁盘、请求内不联网，
// 未缓存的域名直接留空，由前端回退到逐卡代理（那里会现抓并落盘）。
// 响应带内容 ETag 且允许共享缓存，nginx/CDN 可命中，避免每次都回源 NAS。
func (h *FaviconProxyHandler) batchIcons(w http.ResponseWriter, r *http.Request) {
	icons := make(map[string]string)
	total := 0
	var sig strings.Builder
	for _, raw := range strings.Split(r.URL.Query().Get("hosts"), ",") {
		if len(icons) >= batchIconMaxHosts || total >= batchIconMaxBytes {
			break
		}
		host := strings.ToLower(strings.TrimSpace(raw))
		if host == "" || len(host) > 253 || !faviconHostRe.MatchString(host) ||
			strings.Contains(host, "..") || isPrivateHost(host) {
			continue
		}
		if _, dup := icons[host]; dup {
			continue
		}
		e, ok := faviconDataURL(h.cfg, host)
		if !ok {
			continue
		}
		icons[host] = e.url
		total += len(e.url)
		sig.WriteString(host + ":" + strconv.FormatInt(e.size, 10) + ":" +
			strconv.FormatInt(e.mod, 10) + ";")
	}

	etag := `"b` + md5Hex(sig.String()) + `"`
	w.Header().Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Contains(inm, etag) {
		w.Header().Set("Cache-Control", "public, max-age=600")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	// 图标基本不变，允许浏览器与共享缓存短缓存，切组来回时不重复拉
	w.Header().Set("Cache-Control", "public, max-age=600, stale-while-revalidate=600")
	OK(w, map[string]any{"icons": icons})
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
