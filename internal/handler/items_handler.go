package handler

import (
	"context"
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
	"sort"
	"strings"
	"sync"
	"syscall"
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
	reTitle      = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reOGTitle    = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)\s*=\s*["']og:title["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reOGTitle2   = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:property|name)\s*=\s*["']og:title["'][^>]*>`)
	reTwTitle    = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']twitter:title["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reTwTitle2   = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']twitter:title["'][^>]*>`)
	reMetaDesc   = regexp.MustCompile(`(?is)<meta[^>]+(?:name|property)\s*=\s*["']description["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reMetaDesc2  = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:name|property)\s*=\s*["']description["'][^>]*>`)
	reOGDesc     = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)\s*=\s*["']og:description["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reOGDesc2    = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:property|name)\s*=\s*["']og:description["'][^>]*>`)
	reTwDesc     = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']twitter:description["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reTwDesc2    = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']twitter:description["'][^>]*>`)
	reOGImage    = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)\s*=\s*["']og:image["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reOGImage2   = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:property|name)\s*=\s*["']og:image["'][^>]*>`)
	reTwImage    = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']twitter:image["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reTwImage2   = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']twitter:image["'][^>]*>`)
	reKeywords   = regexp.MustCompile(`(?is)<meta[^>]+name\s*=\s*["']keywords["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reKeywords2  = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*name\s*=\s*["']keywords["'][^>]*>`)
	reSiteName   = regexp.MustCompile(`(?is)<meta[^>]+(?:property|name)\s*=\s*["'](?:og:site_name|application-name)["'][^>]*content\s*=\s*["']([^"']+)["'][^>]*>`)
	reSiteName2  = regexp.MustCompile(`(?is)<meta[^>]+content\s*=\s*["']([^"']+)["'][^>]*(?:property|name)\s*=\s*["'](?:og:site_name|application-name)["'][^>]*>`)
	reParagraph  = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	reNoContent  = regexp.MustCompile(`(?is)<(nav|header|footer|aside|noscript|form|svg)\b[^>]*>[\s\S]*?</(nav|header|footer|aside|noscript|form|svg)>`)
	reComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reLinkIcon   = regexp.MustCompile(`(?is)<link\b([^>]*)>`)
	reLinkRel    = regexp.MustCompile(`(?is)\brel\s*=\s*["']([^"']+)["']`)
	reLinkHref   = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
	reLinkSizes  = regexp.MustCompile(`(?is)\bsizes\s*=\s*["']([^"']+)["']`)
	reScriptTag  = regexp.MustCompile(`(?is)<script[^>]*>[\s\S]*?</script>`)
	reStyleTag   = regexp.MustCompile(`(?is)<style[^>]*>[\s\S]*?</style>`)
	reStripTags  = regexp.MustCompile(`<[^>]+>`)
	reMultiSpace = regexp.MustCompile(`\s+`)

	// SVG 清洗规则：上传/抓取/导入的 SVG 均不可信，需移除脚本与可执行协议
	reSvgDangerTag = regexp.MustCompile(`(?is)<(iframe|embed|object|foreignObject)\b[^>]*>[\s\S]*?</(iframe|embed|object|foreignObject)>`)
	reSvgBareTag   = regexp.MustCompile(`(?is)<(iframe|embed|object|script)\b[^>]*/>`)
	reSvgEventAttr = regexp.MustCompile(`(?is)\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	reSvgBadScheme = regexp.MustCompile(`(?is)((?:xlink:)?href\s*=\s*["']\s*)(?:javascript|vbscript|data:text/html)[^"']*`)
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

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	for _, c := range privateCIDRs {
		if c.Contains(ip) {
			return true
		}
	}
	return false
}

func isPrivateHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPrivateIP(ip)
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return true
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return true
		}
	}
	return false
}

// blockPrivateDial 在建立连接时校验真实目标 IP，防止 DNS 重绑定绕过前置域名检查
func blockPrivateDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if isPrivateIP(net.ParseIP(host)) {
		return fmt.Errorf("blocked private address: %s", host)
	}
	return nil
}

// 共享 Transport：连接池复用，避免每次抓取都重新建连（TLS 握手是主要耗时）
var safeHTTPTransport = &http.Transport{
	Proxy: nil, ForceAttemptHTTP2: false,
	MaxIdleConns: 32, MaxIdleConnsPerHost: 6,
	IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	DialContext: (&net.Dialer{
		Timeout:   8 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   blockPrivateDial,
	}).DialContext,
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
}

func newSafeHTTPClient(timeout time.Duration) *http.Client {
	client := &http.Client{Timeout: timeout, Transport: safeHTTPTransport}
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

// 以最终页面 URL 为基准解析相对/协议相对地址，兼容重定向后的域名与子路径
func absolutizeURL(href string, base *url.URL) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	href = htmlDecodeReplacer.Replace(href)
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(u)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	return abs.String()
}

// extractIconLinks 按图标质量排序：大尺寸 apple-touch-icon / 声明 sizes 的 icon 优先，
// 跳过 mask-icon（单色模板）与 preload，最后补 /favicon.ico
func extractIconLinks(html string, base *url.URL) []string {
	type cand struct {
		href  string
		score int
	}
	var cands []cand
	for _, m := range reLinkIcon.FindAllStringSubmatch(html, -1) {
		attrs := m[1]
		relMatch := reLinkRel.FindStringSubmatch(attrs)
		hrefMatch := reLinkHref.FindStringSubmatch(attrs)
		if relMatch == nil || hrefMatch == nil {
			continue
		}
		rel := strings.ToLower(strings.TrimSpace(relMatch[1]))
		if !strings.Contains(rel, "icon") || strings.Contains(rel, "mask-icon") || strings.Contains(rel, "bookmark") {
			continue
		}
		href := absolutizeURL(hrefMatch[1], base)
		if href == "" {
			continue
		}
		score := 10
		if strings.Contains(rel, "apple-touch-icon") {
			score = 45
		} else if strings.Contains(rel, "shortcut") {
			score = 20
		}
		if sm := reLinkSizes.FindStringSubmatch(attrs); sm != nil {
			for _, part := range strings.Split(sm[1], " ") {
				part = strings.ToLower(strings.TrimSpace(part))
				if part == "any" {
					score = 60
					continue
				}
				var w, h int
				if _, err := fmt.Sscanf(part, "%dx%d", &w, &h); err == nil && w > 0 {
					if w >= 96 {
						score += 30
					} else if w >= 48 {
						score += 15
					} else {
						score += w / 16
					}
				}
			}
		}
		cands = append(cands, cand{href: href, score: score})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	seen := make(map[string]bool)
	var out []string
	for _, c := range cands {
		if seen[c.href] {
			continue
		}
		seen[c.href] = true
		out = append(out, c.href)
	}
	ico := base.Scheme + "://" + base.Host + "/favicon.ico"
	if !seen[ico] {
		out = append(out, ico)
	}
	return out
}

func sanitizeSVG(data []byte) []byte {
	s := string(data)
	s = reScriptTag.ReplaceAllString(s, "")
	s = reSvgDangerTag.ReplaceAllString(s, "")
	s = reSvgBareTag.ReplaceAllString(s, "")
	s = reSvgEventAttr.ReplaceAllString(s, "")
	s = reSvgBadScheme.ReplaceAllString(s, `${1}#`)
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

// fetchPageHTML 抓取页面 HTML，返回最终 URL（跟随重定向后）作为相对资源解析基准
func fetchPageHTML(rawURL string) (string, *url.URL, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", nil, fmt.Errorf("unsupported scheme")
	}
	if isPrivateHost(u.Hostname()) {
		return "", nil, fmt.Errorf("private host")
	}
	client := newSafeHTTPClient(8 * time.Second)
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml") {
		return "", nil, fmt.Errorf("not html: %s", ct)
	}
	// meta 信息都在 head，2MB 足够；避免下载整站大页面
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", nil, err
	}
	final := u
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL
	}
	return string(body), final, nil
}

func fetchIconBytes(ctx context.Context, rawURL string, timeout time.Duration) ([]byte, string, error) {
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
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := newSafeHTTPClient(timeout)
	req, err := http.NewRequestWithContext(reqCtx, "GET", rawURL, nil)
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

// trySaveIconFromCandidates 并发探测候选图标（最多 8 个），首个成功即落盘并取消其余请求。
// 串行原实现最坏要等 8s × N，遇到挂掉的公共图标源会非常慢。
func trySaveIconFromCandidates(cfg *config.Config, host string, candidates []string) string {
	seen := make(map[string]bool, len(candidates))
	list := make([]string, 0, len(candidates))
	for _, u := range candidates {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] || isPrivateHostMustParse(u) {
			continue
		}
		seen[u] = true
		list = append(list, u)
		if len(list) >= 8 {
			break
		}
	}
	if len(list) == 0 {
		return ""
	}

	type iconResult struct {
		data []byte
		ct   string
		url  string
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan iconResult, len(list))
	for _, u := range list {
		go func(u string) {
			data, ct, err := fetchIconBytes(ctx, u, 5*time.Second)
			if err != nil {
				ch <- iconResult{url: u}
				return
			}
			ch <- iconResult{data: data, ct: ct, url: u}
		}(u)
	}
	for range list {
		r := <-ch
		if len(r.data) < 50 {
			continue
		}
		ext := detectImageExt(r.data)
		if ext == "" {
			ext = extractExtFromCT(r.ct)
		}
		if ext == "" {
			ext = extFromURL(r.url)
		}
		valid := false
		for _, e := range faviconExts {
			if e == ext {
				valid = true
				break
			}
		}
		if !valid {
			continue
		}
		if saved := saveFavicon(cfg, host, ext, r.data); saved != "" {
			cancel()
			log.Printf("[favicon] host=%s — saved via %s (%d bytes)", host, r.url, len(r.data))
			return saved
		}
	}
	log.Printf("[favicon] host=%s — all %d candidates failed", host, len(list))
	return ""
}

func trySaveIcon(cfg *config.Config, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	host := u.Hostname()
	if isPrivateHost(host) {
		return ""
	}
	if cached := checkFaviconCache(cfg, host); cached != "" {
		return cached
	}
	var candidates []string
	html, base, err := fetchPageHTML(rawURL)
	if err == nil && base != nil {
		host = base.Hostname()
		candidates = extractIconLinks(html, base)
	} else {
		candidates = append(candidates, u.Scheme+"://"+u.Host+"/favicon.ico")
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

func firstMetaContent(html string, patterns ...*regexp.Regexp) string {
	for _, re := range patterns {
		if m := re.FindStringSubmatch(html); m != nil {
			if v := cleanText(htmlDecodeReplacer.Replace(m[1])); v != "" {
				return v
			}
		}
	}
	return ""
}

// cleanText 去掉零宽字符、压缩空白，避免把缩进/换行带进标题与简介
func cleanText(s string) string {
	s = strings.NewReplacer("\u200b", "", "\u200c", "", "\u200d", "", "\ufeff", "", "\u00a0", " ").Replace(s)
	return strings.TrimSpace(reMultiSpace.ReplaceAllString(s, " "))
}

// firstParagraphText 从正文首段提取简介；剔除导航/页脚/脚本等噪声，避免整页文字被当成简介
func firstParagraphText(html string) string {
	cleaned := reComment.ReplaceAllString(html, " ")
	cleaned = reScriptTag.ReplaceAllString(cleaned, " ")
	cleaned = reStyleTag.ReplaceAllString(cleaned, " ")
	cleaned = reNoContent.ReplaceAllString(cleaned, " ")
	for _, m := range reParagraph.FindAllStringSubmatch(cleaned, -1) {
		t := cleanText(reStripTags.ReplaceAllString(m[1], " "))
		if len([]rune(t)) >= 12 {
			return t
		}
	}
	return ""
}

func extractMeta(html string) (title, description string) {
	title = firstMetaContent(html, reOGTitle, reOGTitle2, reTwTitle, reTwTitle2)
	if title == "" {
		if m := reTitle.FindStringSubmatch(html); m != nil {
			title = cleanText(reStripTags.ReplaceAllString(m[1], " "))
		}
	}
	if title == "" {
		title = firstMetaContent(html, reSiteName, reSiteName2)
	}
	// 简介优先级：og → twitter → meta description → 正文首段（不再用 keywords 凑数）
	description = firstMetaContent(html, reOGDesc, reOGDesc2, reTwDesc, reTwDesc2, reMetaDesc, reMetaDesc2)
	if description == "" {
		description = firstParagraphText(html)
	}
	if runes := []rune(title); len(runes) > 50 {
		title = strings.TrimSpace(string(runes[:50]))
	}
	if runes := []rune(description); len(runes) > 120 {
		description = strings.TrimSpace(string(runes[:120])) + "…"
	}
	return title, description
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

func scanItem(row *sql.Row) (*ItemRow, error) {
	var it ItemRow
	err := row.Scan(&it.ID, &it.GroupID, &it.Title, &it.URL, &it.LanURL, &it.Description,
		&it.IconType, &it.IconValue, &it.IconBG, &it.OpenMethod, &it.Sort, &it.IsHidden,
		&it.UserID, &it.CreatedAt)
	return &it, err
}

func (h *ItemsHandler) Favicon(w http.ResponseWriter, r *http.Request) {
	// 会触发服务端外网抓取与落盘，仅编辑者及以上可用
	if s := auth.GetSessionFromRequest(r); s == nil || (s.Role != "admin" && s.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
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

// metaCacheEntry 页面元信息内存缓存：重复点击「自动获取」不再重新抓页面
type metaCacheEntry struct {
	title      string
	desc       string
	host       string // 跟随重定向后的最终域名（图标按该域名落盘）
	candidates []string
	ok         bool
	at         time.Time
}

var (
	metaCache     sync.Map // key -> metaCacheEntry
	iconFailCache sync.Map // host -> time.Time（图标全部失败的冷却，避免反复请求公共源）
)

const (
	metaCacheTTL     = 30 * time.Minute
	metaCacheFailTTL = 2 * time.Minute
	iconFailTTL      = 10 * time.Minute
)

func metaCacheKey(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	return strings.ToLower(u.Scheme + "://" + u.Host + p)
}

func iconFetchFailedRecently(host string) bool {
	v, ok := iconFailCache.Load(host)
	if !ok {
		return false
	}
	if time.Since(v.(time.Time)) < iconFailTTL {
		return true
	}
	iconFailCache.Delete(host)
	return false
}

func (h *ItemsHandler) FetchMeta(w http.ResponseWriter, r *http.Request) {
	// 会触发服务端外网抓取与落盘，仅编辑者及以上可用
	if s := auth.GetSessionFromRequest(r); s == nil || (s.Role != "admin" && s.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
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
	host := strings.ToLower(u.Hostname())
	if isPrivateHost(host) {
		Fail(w, "禁止访问内网地址")
		return
	}

	resp := map[string]any{"title": "", "description": "", "icon": "", "fallback_icon": false, "warn": ""}
	if cached := checkFaviconCache(h.cfg, host); cached != "" {
		resp["icon"] = cached
	}

	key := metaCacheKey(u)
	var entry metaCacheEntry
	hit := false
	if v, ok := metaCache.Load(key); ok {
		e := v.(metaCacheEntry)
		ttl := metaCacheTTL
		if !e.ok {
			ttl = metaCacheFailTTL
		}
		if time.Since(e.at) < ttl {
			entry, hit = e, true
		}
	}

	var candidates []string
	if hit {
		resp["title"] = entry.title
		resp["description"] = entry.desc
		resp["cached"] = true
		candidates = entry.candidates
		if entry.host != "" {
			host = entry.host
			if resp["icon"] == "" {
				if c := checkFaviconCache(h.cfg, host); c != "" {
					resp["icon"] = c
				}
			}
		}
		if !entry.ok {
			resp["warn"] = "站点暂时无法访问，已使用缓存结果"
		}
	} else {
		html, base, ferr := fetchPageHTML(rawURL)
		entry = metaCacheEntry{at: time.Now(), host: host}
		if ferr != nil {
			resp["warn"] = "无法访问目标站点：" + ferr.Error()
		} else {
			entry.ok = true
			if base != nil {
				host = strings.ToLower(base.Hostname())
				entry.host = host
				candidates = extractIconLinks(html, base)
			}
			entry.title, entry.desc = extractMeta(html)
			if entry.title == "" {
				// 连 title 都没有的站点，用域名兜底，保证标题框一定有值
				entry.title = strings.TrimPrefix(host, "www.")
			}
			entry.candidates = candidates
			resp["title"] = entry.title
			resp["description"] = entry.desc
		}
		metaCache.Store(key, entry)
	}

	if resp["icon"] == "" {
		if iconFetchFailedRecently(host) {
			resp["fallback_icon"] = true
		} else {
			cands := append([]string{}, candidates...)
			ico := "https://" + host + "/favicon.ico"
			exists := false
			for _, c := range cands {
				if strings.Contains(c, "/favicon.ico") {
					exists = true
					break
				}
			}
			if !exists {
				cands = append(cands, ico)
			}
			for _, tmpl := range faviconFallbackServices {
				cands = append(cands, fmt.Sprintf(tmpl, host))
			}
			if saved := trySaveIconFromCandidates(h.cfg, host, cands); saved != "" {
				resp["icon"] = saved
				iconFailCache.Delete(host)
			} else {
				// 不再回退成第三方图片外链：前端保留 favicon 模式，
				// 由 /api/favicon.php 代理兜底（本地缓存 → 公共源 → 首字母 SVG）
				iconFailCache.Store(host, time.Now())
				resp["fallback_icon"] = true
			}
		}
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
		ID          uint   `json:"id"`
		GroupID     uint   `json:"group_id"`
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
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if body.GroupID == 0 {
		Fail(w, "请选择分组")
		return
	}
	var groupExists int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM item_groups WHERE id=?", body.GroupID).Scan(&groupExists); err != nil || groupExists == 0 {
		Fail(w, "分组不存在")
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

	if body.Dir != "up" && body.Dir != "down" {
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
	tx, err := db.DB.Begin()
	if err != nil {
		Fail(w, "排序失败")
		return
	}
	defer tx.Rollback()
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
