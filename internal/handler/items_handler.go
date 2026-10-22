package handler

import (
	"crypto/md5"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

type ItemsHandler struct {
	cfg *config.Config
}

func NewItemsHandler(cfg *config.Config) *ItemsHandler {
	return &ItemsHandler{cfg: cfg}
}

// ItemRow 卡片行
type ItemRow struct {
	ID          int    `json:"id"`
	GroupID     int    `json:"group_id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	LanURL      string `json:"lan_url"`
	Description string `json:"description"`
	IconType    string `json:"icon_type"`
	IconValue   string `json:"icon_value"`
	IconBG      string `json:"icon_bg"`
	OpenMethod  int    `json:"open_method"`
	Sort        int    `json:"sort"`
	IsHidden    int    `json:"is_hidden"`
	UserID      int    `json:"user_id"`
	CreatedAt   string `json:"created_at"`
}

func isValidURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
func isValidIconType(s string) bool {
	return s == "image" || s == "text" || s == "favicon"
}
func isValidOpenMethod(n int) bool {
	return n == 1 || n == 2 || n == 3
}

var htmlDecodeReplacer = strings.NewReplacer(
	"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'", "&nbsp;", " ",
)

var (
	reTitle     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reOGTitle   = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)\s*=\s*["']og:title["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reOGTitle2  = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:property|name)\s*=\s*["']og:title["'][^>]*>`)
	reTwTitle   = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']twitter:title["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reTwTitle2  = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']twitter:title["'][^>]*>`)
	reMetaDesc  = regexp.MustCompile(`(?is)<meta[^>]+(?:name|property)\s*=\s*["']description["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reMetaDesc2 = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:name|property)\s*=\s*["']description["'][^>]*>`)
	reOGDesc    = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)\s*=\s*["']og:description["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reOGDesc2   = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:property|name)\s*=\s*["']og:description["'][^>]*>`)
	reTwDesc    = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']twitter:description["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reTwDesc2   = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']twitter:description["'][^>]*>`)
	reOGImage   = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)\s*=\s*["']og:image["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reOGImage2  = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:property|name)\s*=\s*["']og:image["'][^>]*>`)
	reTwImage   = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']twitter:image["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reTwImage2  = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']twitter:image["'][^>]*>`)
	reKeywords  = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']keywords["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reKeywords2 = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']keywords["'][^>]*>`)
	reLinkIcon  = regexp.MustCompile(`(?is)<link\b([^>]*)>`)
	reScriptTag = regexp.MustCompile(`(?is)<script[^>]*>[\s\S]*?</script>`)
	reStyleTag  = regexp.MustCompile(`(?is)<style[^>]*>[\s\S]*?</style>`)
	reStripTags = regexp.MustCompile(`<[^>]+>`)
	reMultiSpace = regexp.MustCompile(`\s+`)
)

var privateCIDRs []*net.IPNet

func init() {
	cidrs := []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "0.0.0.0/8", "169.254.0.0/16"}
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err == nil {
			privateCIDRs = append(privateCIDRs, n)
		}
	}
}

func isPrivateHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, c := range privateCIDRs {
			if c.Contains(ip) {
				return true
			}
		}
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return true
	}
	for _, ip := range ips {
		for _, c := range privateCIDRs {
			if c.Contains(ip) {
				return true
			}
		}
	}
	return false
}

func newSafeHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: false, MaxIdleConns: 10,
		IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Timeout: timeout, Transport: transport}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to non-http scheme")
		}
		if isPrivateHost(req.URL.Hostname()) {
			return fmt.Errorf("redirect to private IP")
		}
		return nil
	}
	return client
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func absolutize(href, scheme, host string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	href = htmlDecodeReplacer.Replace(href)
	if strings.HasPrefix(href, "//") {
		return scheme + ":" + href
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	if strings.HasPrefix(href, "/") {
		return scheme + "://" + host + href
	}
	return scheme + "://" + host + "/" + strings.TrimLeft(href, "./")
}

func extractIconLinks(html, scheme, host string) []string {
	var results []string
	seen := make(map[string]bool)
	add := func(u string) {
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		results = append(results, u)
	}

	matches := reLinkIcon.FindAllStringSubmatch(html, -1)
	type cand struct{ rel, href string }
	var cands []cand
	for _, m := range matches {
		attrs := m[1]
		relRe := regexp.MustCompile(`(?is)\brel\s*=\s*["']([^"']+)["']`)
		hrefRe := regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
		relMatch := relRe.FindStringSubmatch(attrs)
		hrefMatch := hrefRe.FindStringSubmatch(attrs)
		if relMatch == nil || hrefMatch == nil {
			continue
		}
		rel := strings.ToLower(strings.TrimSpace(relMatch[1]))
		href := strings.TrimSpace(hrefMatch[1])
		cands = append(cands, cand{rel: rel, href: href})
	}

	for _, c := range cands {
		if strings.Contains(c.rel, "icon") {
			add(absolutize(c.href, scheme, host))
		}
	}
	for _, c := range cands {
		if c.rel == "shortcut icon" || c.rel == "shortcut" {
			add(absolutize(c.href, scheme, host))
		}
	}
	add(scheme + "://" + host + "/favicon.ico")
	return results
}

func sanitizeSVG(data []byte) []byte {
	s := string(data)
	s = reScriptTag.ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?is)\son\w+\s*=\s*"[^"]*"`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`(?is)<foreignObject\b[^>]*>[\s\S]*?</foreignObject>`).ReplaceAllString(s, "")
	return []byte(s)
}

func extFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	p := u.Path
	idx := strings.LastIndex(p, ".")
	if idx == -1 {
		return ""
	}
	ext := strings.ToLower(p[idx:])
	if len(ext) > 6 {
		return ""
	}
	return ext
}

func fetchPageHTML(rawURL string) (html, scheme, host string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", err
	}
	scheme = u.Scheme
	host = u.Hostname()
	if scheme != "http" && scheme != "https" {
		return "", "", "", fmt.Errorf("unsupported scheme")
	}
	if isPrivateHost(host) {
		return "", "", "", fmt.Errorf("private host")
	}
	client := newSafeHTTPClient(10 * time.Second)
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", "", "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "text/html") && !strings.Contains(strings.ToLower(ct), "application/xhtml") {
		return "", "", "", fmt.Errorf("not html: %s", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return "", "", "", err
	}
	return string(body), scheme, host, nil
}

func fetchIconBytes(rawURL string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", fmt.Errorf("unsupported scheme")
	}
	if isPrivateHost(u.Hostname()) {
		return nil, "", fmt.Errorf("private host")
	}
	client := newSafeHTTPClient(8 * time.Second)
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	cl := resp.Header.Get("Content-Length")
	if cl != "" {
		var n int64
		fmt.Sscanf(cl, "%d", &n)
		if n > 2*1024*1024 {
			return nil, "", fmt.Errorf("too large")
		}
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > 2*1024*1024 {
		return nil, "", fmt.Errorf("too large")
	}
	return body, ct, nil
}

var faviconExts = []string{".png", ".ico", ".svg", ".jpg", ".gif", ".webp"}

func checkFaviconCache(cfg *config.Config, host string) string {
	md5h := md5Hex(host)
	dir := filepath.Join(cfg.UploadDir, "favicon")
	for _, ext := range faviconExts {
		p := filepath.Join(dir, md5h+ext)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > 30*24*time.Hour {
			os.Remove(p)
			continue
		}
		return "/frontend/uploads/favicon/" + md5h + ext
	}
	return ""
}

func saveFavicon(cfg *config.Config, host, ext string, data []byte) string {
	md5h := md5Hex(host)
	dir := filepath.Join(cfg.UploadDir, "favicon")
	os.MkdirAll(dir, 0755)
	if ext == "" {
		ext = ".png"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	validExt := false
	for _, e := range faviconExts {
		if e == ext {
			validExt = true
			break
		}
	}
	if !validExt {
		ext = ".png"
	}
	if ext == ".svg" {
		data = sanitizeSVG(data)
	}
	p := filepath.Join(dir, md5h+ext)
	if err := os.WriteFile(p, data, 0644); err != nil {
		return ""
	}
	info, err := os.Stat(p)
	if err != nil || info.Size() < 50 {
		os.Remove(p)
		return ""
	}
	return "/frontend/uploads/favicon/" + md5h + ext
}

var faviconFallbackServices = []string{
	"https://api.iowen.cn/favicon/%s.png",
	"https://favicon.cccyun.cc/%s",
	"https://favicon.im/%s?larger=true",
	"https://icon.horse/icon/%s",
	"https://www.google.com/s2/favicons?domain=%s&sz=64",
}

func extractExtFromCT(ct string) string {
	switch {
	case strings.Contains(ct, "svg"):
		return ".svg"
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "gif"):
		return ".gif"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "x-icon") || strings.Contains(ct, "icon"):
		return ".ico"
	default:
		return ""
	}
}

func detectImageExt(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return ".png"
	}
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return ".jpg"
	}
	if data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x38 {
		return ".gif"
	}
	if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x01 && data[3] == 0x00 {
		return ".ico"
	}
	if len(data) >= 12 && data[0] == 0x52 && data[1] == 0x49 && data[2] == 0x46 && data[3] == 0x46 &&
		data[8] == 0x57 && data[9] == 0x45 && data[10] == 0x42 && data[11] == 0x50 {
		return ".webp"
	}
	if strings.Contains(string(data[:min(len(data), 512)]), "<svg") {
		return ".svg"
	}
	return ""
}

func trySaveIconFromCandidates(cfg *config.Config, host string, candidates []string) string {
	for _, iconURL := range candidates {
		if iconURL == "" {
			continue
		}
		if isPrivateHostMustParse(iconURL) {
			continue
		}
		data, ct, err := fetchIconBytes(iconURL)
		if err != nil {
			log.Printf("[favicon] host=%s url=%s — %v", host, iconURL, err)
			continue
		}
		if len(data) < 50 {
			log.Printf("[favicon] host=%s url=%s — too small (%d bytes)", host, iconURL, len(data))
			continue
		}
		ext := detectImageExt(data)
		if ext == "" {
			ext = extractExtFromCT(ct)
		}
		if ext == "" {
			ext = extFromURL(iconURL)
		}
		validExt := false
		for _, e := range faviconExts {
			if e == ext {
				validExt = true
				break
			}
		}
		if !validExt {
			log.Printf("[favicon] host=%s url=%s — unsupported ext=%s", host, iconURL, ext)
			continue
		}
		saved := saveFavicon(cfg, host, ext, data)
		if saved != "" {
			log.Printf("[favicon] host=%s — saved via %s (%d bytes)", host, iconURL, len(data))
			return saved
		}
	}
	log.Printf("[favicon] host=%s — ALL candidates failed, will use fallback service", host)
	return ""
}

func trySaveIcon(cfg *config.Config, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	scheme := u.Scheme
	host := u.Hostname()
	if scheme != "http" && scheme != "https" {
		return ""
	}
	if isPrivateHost(host) {
		return ""
	}
	cached := checkFaviconCache(cfg, host)
	if cached != "" {
		return cached
	}
	var candidates []string
	html, s, h, err := fetchPageHTML(rawURL)
	if err == nil {
		scheme = s
		host = h
		candidates = extractIconLinks(html, scheme, host)
	} else {
		candidates = append(candidates, scheme+"://"+host+"/favicon.ico")
	}
	for _, tmpl := range faviconFallbackServices {
		candidates = append(candidates, fmt.Sprintf(tmpl, host))
	}
	return trySaveIconFromCandidates(cfg, host, candidates)
}

func isPrivateHostMustParse(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	return isPrivateHost(u.Hostname())
}

func fallbackFaviconURL(host string) string {
	for _, tmpl := range faviconFallbackServices {
		return fmt.Sprintf(tmpl, host)
	}
	return ""
}

func extractMeta(html string) (title, description, favicon string) {
	title = ""
	description = ""
	if m := reOGTitle.FindStringSubmatch(html); m != nil {
		title = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	} else if m := reOGTitle2.FindStringSubmatch(html); m != nil {
		title = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	} else if m := reTwTitle.FindStringSubmatch(html); m != nil {
		title = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	} else if m := reTwTitle2.FindStringSubmatch(html); m != nil {
		title = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	}
	if title == "" {
		if m := reTitle.FindStringSubmatch(html); m != nil {
			t := reStripTags.ReplaceAllString(m[1], "")
			title = strings.TrimSpace(htmlDecodeReplacer.Replace(t))
		}
	}
	if m := reOGDesc.FindStringSubmatch(html); m != nil {
		description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	} else if m := reOGDesc2.FindStringSubmatch(html); m != nil {
		description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	} else if m := reTwDesc.FindStringSubmatch(html); m != nil {
		description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	} else if m := reTwDesc2.FindStringSubmatch(html); m != nil {
		description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
	}
	if description == "" {
		if m := reMetaDesc.FindStringSubmatch(html); m != nil {
			description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
		} else if m := reMetaDesc2.FindStringSubmatch(html); m != nil {
			description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
		}
	}
	if description == "" {
		if m := reKeywords.FindStringSubmatch(html); m != nil {
			description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
		} else if m := reKeywords2.FindStringSubmatch(html); m != nil {
			description = strings.TrimSpace(htmlDecodeReplacer.Replace(m[1]))
		}
	}
	if description == "" {
		text := reScriptTag.ReplaceAllString(html, " ")
		text = reStyleTag.ReplaceAllString(text, " ")
		text = reStripTags.ReplaceAllString(text, " ")
		text = reMultiSpace.ReplaceAllString(text, " ")
		text = strings.TrimSpace(htmlDecodeReplacer.Replace(text))
		runes := []rune(text)
		if len(runes) > 80 {
			description = string(runes[:80])
		} else {
			description = text
		}
	}
	title = strings.TrimSpace(reMultiSpace.ReplaceAllString(title, " "))
	description = strings.TrimSpace(reMultiSpace.ReplaceAllString(description, " "))
	if runes := []rune(title); len(runes) > 50 {
		title = string(runes[:50])
	}
	if runes := []rune(description); len(runes) > 200 {
		description = string(runes[:200])
	}
	return title, description, favicon
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ===== Dispatch 与 CRUD =====

func (h *ItemsHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	switch action {
	case "", "list":
		h.List(w, r)
	case "edit":
		h.Edit(w, r)
	case "delete":
		h.Delete(w, r)
	case "sort":
		h.Sort(w, r)
	case "sort_batch":
		h.SortBatch(w, r)
	case "toggle_hidden":
		h.ToggleHidden(w, r)
	case "favicon":
		h.Favicon(w, r)
	case "fetch_meta":
		h.FetchMeta(w, r)
	default:
		Fail(w, "未知操作")
	}
}

func (h *ItemsHandler) PublicDispatch(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	switch action {
	case "", "list":
		h.List(w, r)
	case "favicon":
		h.Favicon(w, r)
	case "fetch_meta":
		h.FetchMeta(w, r)
	default:
		Fail(w, "未知操作")
	}
}

func scanItem(row *sql.Row) (*ItemRow, error) {
	var it ItemRow
	err := row.Scan(&it.ID, &it.GroupID, &it.Title, &it.URL, &it.LanURL, &it.Description,
		&it.IconType, &it.IconValue, &it.IconBG, &it.OpenMethod, &it.Sort, &it.IsHidden,
		&it.UserID, &it.CreatedAt)
	return &it, err
}

func (h *ItemsHandler) Favicon(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		Fail(w, "url 不能为空")
		return
	}
	if !isValidURL(rawURL) {
		Fail(w, "URL 必须以 http:// 或 https:// 开头")
		return
	}
	if h.cfg == nil {
		Fail(w, "handler 未初始化")
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		Fail(w, "URL 解析失败")
		return
	}
	host := u.Hostname()
	if isPrivateHost(host) {
		Fail(w, "禁止访问内网地址")
		return
	}
	cached := checkFaviconCache(h.cfg, host)
	if cached != "" {
		OK(w, map[string]any{"url": cached, "cached": true, "fallback": false})
		return
	}
	result := trySaveIcon(h.cfg, rawURL)
	if result != "" {
		OK(w, map[string]any{"url": result, "cached": strings.HasPrefix(result, "/frontend/uploads/"), "fallback": false})
		return
	}
	fallbackURL := fallbackFaviconURL(host)
	OK(w, map[string]any{"url": fallbackURL, "cached": false, "fallback": true, "diag": "无法抓取目标站点图标，已改用公共图标服务"})
}

func (h *ItemsHandler) FetchMeta(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		Fail(w, "url 不能为空")
		return
	}
	if !isValidURL(rawURL) {
		Fail(w, "URL 必须以 http:// 或 https:// 开头")
		return
	}
	if h.cfg == nil {
		Fail(w, "handler 未初始化")
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		Fail(w, "URL 解析失败")
		return
	}
	host := u.Hostname()
	if isPrivateHost(host) {
		Fail(w, "禁止访问内网地址")
		return
	}
	resp := map[string]any{"title": "", "description": "", "icon": "", "fallback_icon": false, "warn": ""}
	scheme := u.Scheme
	if scheme != "http" && scheme != "https" {
		scheme = "https"
	}
	cached := checkFaviconCache(h.cfg, host)
	if cached != "" {
		resp["icon"] = cached
	}
	var candidates []string
	html, pageScheme, pageHost, err := fetchPageHTML(rawURL)
	if err == nil {
		title, desc, _ := extractMeta(html)
		resp["title"] = title
		resp["description"] = desc
		scheme = pageScheme
		candidates = extractIconLinks(html, pageScheme, pageHost)
		addMetaImg := func(patterns ...*regexp.Regexp) {
			for _, re := range patterns {
				if m := re.FindStringSubmatch(html); m != nil {
					abs := absolutize(strings.TrimSpace(htmlDecodeReplacer.Replace(m[1])), scheme, host)
					if abs != "" {
						seen := false
						for _, c := range candidates {
							if c == abs {
								seen = true
								break
							}
						}
						if !seen {
							candidates = append(candidates, abs)
						}
					}
				}
			}
		}
		addMetaImg(reOGImage, reOGImage2, reTwImage, reTwImage2)
	} else {
		resp["warn"] = "无法访问目标站点：" + err.Error()
	}
	if resp["icon"] == "" {
		hasFaviconIco := false
		for _, c := range candidates {
			if strings.Contains(c, "/favicon.ico") {
				hasFaviconIco = true
				break
			}
		}
		if !hasFaviconIco {
			candidates = append(candidates, scheme+"://"+host+"/favicon.ico")
		}
		for _, tmpl := range faviconFallbackServices {
			candidates = append(candidates, fmt.Sprintf(tmpl, host))
		}
		if saved := trySaveIconFromCandidates(h.cfg, host, candidates); saved != "" {
			resp["icon"] = saved
		}
	}
	if resp["icon"] == "" {
		resp["icon"] = fallbackFaviconURL(host)
		resp["fallback_icon"] = true
	}
	OK(w, resp)
}

func (h *ItemsHandler) List(w http.ResponseWriter, r *http.Request) {
	query := "SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items"
	args := []any{}
	if gid := r.URL.Query().Get("group_id"); gid != "" {
		query += " WHERE group_id = ?"
		args = append(args, gid)
	}
	query += " ORDER BY sort ASC, id ASC"
	rows, err := db.DB.Query(query, args...)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取卡片失败")
		return
	}
	defer rows.Close()
	var items []ItemRow
	for rows.Next() {
		var it ItemRow
		rows.Scan(&it.ID, &it.GroupID, &it.Title, &it.URL, &it.LanURL, &it.Description,
			&it.IconType, &it.IconValue, &it.IconBG, &it.OpenMethod, &it.Sort, &it.IsHidden,
			&it.UserID, &it.CreatedAt)
		items = append(items, it)
	}
	if items == nil {
		items = []ItemRow{}
	}
	OK(w, items)
}

func (h *ItemsHandler) Edit(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID         uint   `json:"id"`
		GroupID    uint   `json:"group_id"`
		Title      string `json:"title"`
		URL        string `json:"url"`
		LanURL     string `json:"lan_url"`
		Description string `json:"description"`
		IconType   string `json:"icon_type"`
		IconValue  string `json:"icon_value"`
		IconBG     string `json:"icon_bg"`
		OpenMethod int    `json:"open_method"`
		Sort       int    `json:"sort"`
		IsHidden   int    `json:"is_hidden"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if body.GroupID == 0 {
		Fail(w, "请选择分组")
		return
	}
	if body.Title == "" {
		Fail(w, "标题不能为空")
		return
	}
	if body.URL != "" && !isValidURL(body.URL) {
		Fail(w, "URL 必须以 http:// 或 https:// 开头")
		return
	}
	if body.LanURL != "" && !isValidURL(body.LanURL) {
		Fail(w, "LAN URL 必须以 http:// 或 https:// 开头")
		return
	}
	if !isValidIconType(body.IconType) {
		Fail(w, "icon_type 必须是 image/text/favicon")
		return
	}
	if body.OpenMethod == 0 {
		body.OpenMethod = 2
	}
	if !isValidOpenMethod(body.OpenMethod) {
		Fail(w, "open_method 必须是 1/2/3")
		return
	}
	if body.IsHidden != 1 {
		body.IsHidden = 0
	}

	if body.ID == 0 {
		res, err := db.DB.Exec("INSERT INTO items (group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
			body.GroupID, body.Title, body.URL, body.LanURL, body.Description, body.IconType, body.IconValue, body.IconBG, body.OpenMethod, body.Sort, body.IsHidden)
		if err != nil {
			Fail(w, "创建失败")
			return
		}
		lastID, _ := res.LastInsertId()
		it, _ := scanItem(db.DB.QueryRow("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items WHERE id=?", lastID))
		OK(w, it)
		return
	}

	_, err := db.DB.Exec("UPDATE items SET group_id=?, title=?, url=?, lan_url=?, description=?, icon_type=?, icon_value=?, icon_bg=?, open_method=?, sort=?, is_hidden=? WHERE id=?",
		body.GroupID, body.Title, body.URL, body.LanURL, body.Description, body.IconType, body.IconValue, body.IconBG, body.OpenMethod, body.Sort, body.IsHidden, body.ID)
	if err != nil {
		Fail(w, "保存失败")
		return
	}
	it, err := scanItem(db.DB.QueryRow("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items WHERE id=?", body.ID))
	if err != nil {
		Fail(w, "卡片不存在")
		return
	}
	OK(w, it)
}

func (h *ItemsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID uint `json:"id"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}
	db.DB.Exec("DELETE FROM items WHERE id=?", body.ID)
	OK(w, nil)
}

func (h *ItemsHandler) Sort(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID  uint   `json:"id"`
		Dir string `json:"dir"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}

	var curGroupID, curSort int
	err := db.DB.QueryRow("SELECT group_id, sort FROM items WHERE id=?", body.ID).Scan(&curGroupID, &curSort)
	if err != nil {
		Fail(w, "卡片不存在")
		return
	}

	var otherSort int
	if body.Dir == "up" {
		err = db.DB.QueryRow("SELECT sort FROM items WHERE group_id=? AND sort < ? ORDER BY sort DESC, id DESC LIMIT 1", curGroupID, curSort).Scan(&otherSort)
	} else {
		err = db.DB.QueryRow("SELECT sort FROM items WHERE group_id=? AND sort > ? ORDER BY sort ASC, id ASC LIMIT 1", curGroupID, curSort).Scan(&otherSort)
	}
	if err != nil {
		Fail(w, map[string]string{"up": "已经是第一个", "down": "已经是最后一个"}[body.Dir])
		return
	}
	db.DB.Exec("UPDATE items SET sort=? WHERE id=?", otherSort, body.ID)
	var otherID int
	if body.Dir == "up" {
		db.DB.QueryRow("SELECT id FROM items WHERE group_id=? AND sort=? AND id!=? ORDER BY sort ASC, id ASC LIMIT 1", curGroupID, otherSort, body.ID).Scan(&otherID)
	} else {
		db.DB.QueryRow("SELECT id FROM items WHERE group_id=? AND sort=? AND id!=? ORDER BY sort DESC, id DESC LIMIT 1", curGroupID, otherSort, body.ID).Scan(&otherID)
	}
	if otherID > 0 {
		db.DB.Exec("UPDATE items SET sort=? WHERE id=?", curSort, otherID)
	}
	OK(w, nil)
}

func (h *ItemsHandler) SortBatch(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		IDs []uint `json:"ids"`
	}
	if err := BindJSON(r, &body); err != nil || len(body.IDs) == 0 {
		Fail(w, "参数错误")
		return
	}
	tx, _ := db.DB.Begin()
	for i, id := range body.IDs {
		if _, err := tx.Exec("UPDATE items SET sort=? WHERE id=?", i, id); err != nil {
			tx.Rollback()
			Fail(w, "排序失败")
			return
		}
	}
	tx.Commit()
	OK(w, nil)
}

func (h *ItemsHandler) ToggleHidden(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		ID       uint `json:"id"`
		IsHidden int  `json:"is_hidden"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}
	if body.IsHidden != 0 && body.IsHidden != 1 {
		body.IsHidden = 0
	}

	tx, err := db.DB.Begin()
	if err != nil {
		Fail(w, "保存失败")
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec("UPDATE items SET is_hidden=? WHERE id=?", body.IsHidden, body.ID)
	if err != nil {
		Fail(w, "保存失败")
		return
	}
	it, err := scanItem(tx.QueryRow("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items WHERE id=?", body.ID))
	if err != nil {
		Fail(w, "卡片不存在")
		return
	}
	if err := tx.Commit(); err != nil {
		Fail(w, "保存失败")
		return
	}
	OK(w, it)
}
