package handler

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

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

	if disk, _, ext := faviconCachePath(h.cfg, host); disk != "" {
		h.serveFile(w, disk, ext)
		return
	}

	slot := &faviconFetchSlot{done: make(chan struct{})}
	actual, loaded := faviconInflight.LoadOrStore(host, slot)
	if loaded {
		wait := actual.(*faviconFetchSlot)
		<-wait.done
		if wait.path != "" {
			if disk, _, e := faviconCachePath(h.cfg, host); disk != "" {
				h.serveFile(w, disk, e)
				return
			}
		}
		http.Redirect(w, r, fallbackFaviconURL(host), http.StatusFound)
		return
	}

	candidates := []string{"https://" + host + "/favicon.ico"}
	for _, tmpl := range faviconFallbackServices {
		candidates = append(candidates, strings.ReplaceAll(tmpl, "%s", host))
	}
	log.Printf("[favicon] host=%s — trying %d candidates", host, len(candidates))
	slot.path = trySaveIconFromCandidates(h.cfg, host, candidates)
	close(slot.done)
	faviconInflight.Delete(host)

	if disk, _, e := faviconCachePath(h.cfg, host); disk != "" {
		h.serveFile(w, disk, e)
		return
	}
	log.Printf("[favicon] host=%s — all candidates + fallback failed, serving inline SVG", host)
	h.serveFallbackSVG(w, host)
}

// fallbackSVG 当整个 favicon 解析链都挂了时返回一个带首字母的 SVG 占位
func (h *FaviconProxyHandler) serveFallbackSVG(w http.ResponseWriter, host string) {
	// 取 host 首段首字符（如 "github.com" → "G"，"zhihu.com" → "Z"）
	label := ""
	if dot := strings.Index(host, "."); dot > 0 {
		label = strings.ToUpper(host[:1])
	} else if len(host) > 0 {
		label = strings.ToUpper(host[:1])
	}
	svg := fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="6" fill="#6c5ce7"/><text x="50%%" y="54%%" text-anchor="middle" dominant-baseline="middle" font-family="system-ui,Helvetica,Arial" font-size="16" font-weight="700" fill="#ffffff">%s</text></svg>`,
		label,
	)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(svg))
}

func (h *FaviconProxyHandler) serveFile(w http.ResponseWriter, diskPath, ext string) {
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
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
