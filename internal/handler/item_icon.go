package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/config"
)

// 站点图标链路（获取侧对标 Sun-Panel，存储侧沿用本项目）：
//   1. GET 站点页面，解析 <link rel="...icon..."> / manifest 得到候选列表；
//   2. 手动模式：候选列表给用户点选，选中后单个下载落盘；
//   3. 自动模式（自动获取 / 批量补图）：先查本地缓存 → 候选并发抓取 → 按声明质量与实际像素选优；
//   4. 站点自身抓不到（反爬 403、无图标、证书异常）时用公共图标源兜底。
// 存储层不变：按 host 去重、魔数校验、位图缩到 ≤64px 转 WebP、SVG 清洗。

const faviconMaxBytes = 0x100000 // 1MB

// siteFaviconTimeout 单次页面/图标请求的超时
const siteFaviconTimeout = 10 * time.Second

// faviconUserAgent 不少站点/CDN 对 Go 默认 UA（Go-http-client）直接返回 403/405，
// 用浏览器 UA 抓取可显著提高 favicon.ico / 页面图标的成功率。
const faviconUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"

// faviconClient 带超时且禁连回环/链路本地的客户端（复用 safeHTTPTransport，防 SSRF）
func faviconClient() *http.Client {
	return newSafeHTTPClient(siteFaviconTimeout)
}

// parseSiteURL 校验并解析站点地址；仅拦截回环/链路本地，内网 NAS 允许
func parseSiteURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("URL 解析失败")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("URL 必须以 http:// 或 https:// 开头")
	}
	if isPrivateHost(u.Hostname()) {
		return nil, fmt.Errorf("禁止访问本机回环或链路本地地址")
	}
	return u, nil
}

// fetchFaviconPage 抓取页面 HTML，返回 HTML 与跟随重定向后的最终 URL（相对路径解析基准）。
// 只接受 parseSiteURL 校验过的地址（scheme 白名单 + 禁止回环/链路本地），避免把未校验的
// 用户输入直接用于出站请求。
// 站点返回 4xx/5xx（反爬页、防盗链）或非 HTML 时不算失败：返回空 HTML + 最终 URL，
// 由调用方用默认路径候选（/favicon.ico、apple-touch-icon 等）继续尝试。
func fetchFaviconPage(pageURL *url.URL) (string, *url.URL) {
	if pageURL == nil || pageURL.Host == "" {
		return "", nil
	}
	req, err := http.NewRequest("GET", pageURL.String(), nil)
	if err != nil {
		return "", nil
	}
	req.Header.Set("User-Agent", faviconUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	// 出站目标即「获取站点图标 / 自动获取标题」功能本身，仅管理员与编辑者可调用；
	// 已经过 scheme 白名单、回环 / 链路本地拦截、重定向复检，并在拨号阶段校验真实 IP
	// （防 DNS 重绑定），因此属于预期内的受控请求，非可被访客利用的请求伪造。
	resp, err := faviconClient().Do(req) // lgtm[go/request-forgery]
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()

	base := req.URL
	if resp.Request != nil && resp.Request.URL != nil {
		base = resp.Request.URL
	}
	if resp.StatusCode != http.StatusOK {
		return "", base
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if ct != "" && !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
		return "", base
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", base
	}
	return string(body), base
}

// siteFaviconCandidates 返回站点的图标候选地址（供后台点选，按声明质量排序）。
func siteFaviconCandidates(rawURL string) ([]string, error) {
	base, err := parseSiteURL(rawURL)
	if err != nil {
		return nil, err
	}
	html, final := fetchFaviconPage(base)
	if final != nil {
		base = final
	}
	cands := iconCandidatesFromHTML(html, base)
	if len(cands) == 0 {
		return nil, fmt.Errorf("未找到可用的站点图标")
	}
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.URL)
	}
	return out, nil
}

// downloadFavicon 下载用户选中的图标。
// 不再先发 HEAD：不少 CDN 对 HEAD 返回 403/405，会把本可下载的图标误判成失败；
// 体积上限由 LimitReader 兜底，多读的流量可忽略。
func downloadFavicon(cfg *config.Config, host, iconURL string) (string, error) {
	u, err := parseSiteURL(iconURL)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", fmt.Errorf("handler 未初始化")
	}

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", faviconUserAgent)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := faviconClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("图标地址返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > faviconMaxBytes {
		return "", fmt.Errorf("图标超过 1MB")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, faviconMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > faviconMaxBytes {
		return "", fmt.Errorf("图标超过 1MB")
	}
	if len(data) == 0 {
		return "", fmt.Errorf("图标内容为空")
	}

	// 存储层：按站点 host 去重（同一站点重复抓取覆盖旧文件），
	// 统一做魔数校验 + 缩放到 ≤64px + 转 WebP，避免把 1MB 原图塞进首屏。
	if host == "" {
		host = u.Hostname()
	}
	ext := detectImageExt(data)
	if ext == "" {
		ext = extractExtFromCT(resp.Header.Get("Content-Type"))
	}
	if ext == "" {
		ext = extFromURL(iconURL)
	}
	saved := saveFavicon(cfg, host, ext, data)
	if saved == "" {
		return "", fmt.Errorf("图标内容无效或无法识别")
	}
	// 手动成功一次即解除冷却，避免自动获取被之前的失败记录挡住
	clearIconFetchFailed(host)
	return saved, nil
}

// autoPickFavicon 自动选优：本地缓存 → 页面声明图标/默认路径 → 公共图标源。
// html 为空（页面抓不到）时只用默认路径 + 公共源，保证反爬站点尽量也能补上图。
func autoPickFavicon(cfg *config.Config, host string, base *url.URL, html string) (string, error) {
	if cfg == nil || base == nil {
		return "", fmt.Errorf("站点地址无效")
	}
	if host == "" {
		host = strings.ToLower(base.Hostname())
	}
	// 1) 本地已有该站点图标：直接复用，0 网络请求
	if cached := checkFaviconCache(cfg, host); cached != "" {
		return cached, nil
	}
	// 2) 冷却期内不再出站，避免坏站点被反复重试
	if iconFetchFailedRecently(host) {
		return "", fmt.Errorf("该站点图标抓取刚失败，稍后再试")
	}
	if saved := fetchBestIcon(cfg, host, iconCandidatesFromHTML(html, base)); saved != "" {
		return saved, nil
	}
	if saved, err := autoPickFaviconFallback(cfg, host); err == nil {
		return saved, nil
	}
	markIconFetchFailed(host)
	return "", fmt.Errorf("未找到可用的站点图标")
}

// autoPickFaviconFallback 站点自身抓不到图标时的兜底：公共图标源只接受位图，
// 避免把「字母占位 SVG」当成真实 logo 存进卡片。
func autoPickFaviconFallback(cfg *config.Config, host string) (string, error) {
	if cfg == nil || host == "" {
		return "", fmt.Errorf("站点地址无效")
	}
	cands := make([]iconCandidate, 0, len(faviconFallbackServices))
	for i, tmpl := range faviconFallbackServices {
		cands = append(cands, iconCandidate{URL: fmt.Sprintf(tmpl, host), Score: 12 - i, RasterOnly: true})
	}
	if saved := fetchBestIcon(cfg, host, cands); saved != "" {
		return saved, nil
	}
	return "", fmt.Errorf("未找到可用的站点图标")
}

// autoPickFaviconFromURL 只有页面地址时使用（批量补图 / 迁移）：抓页面后自动选优。
// 先查本地缓存与失败冷却，命中则完全不发请求（批量补图时省掉大量重复出站）。
func autoPickFaviconFromURL(cfg *config.Config, pageURL string) (string, error) {
	base, err := parseSiteURL(pageURL)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(base.Hostname())
	if cached := checkFaviconCache(cfg, host); cached != "" {
		return cached, nil
	}
	if iconFetchFailedRecently(host) {
		return "", fmt.Errorf("该站点图标抓取刚失败，稍后再试")
	}
	html, final := fetchFaviconPage(base)
	if final != nil {
		base = final
	}
	return autoPickFavicon(cfg, hostOfURL(base.String()), base, html)
}

/* ================= 本地缓存命中 + 失败冷却 ================= */

// iconFetchFailTTL 图标抓取失败后的冷却时长：同一站点短时间内不再重复出站请求，
// 避免连续点「自动获取」或批量补图时对坏站点反复撞墙（原来的 favicon 负缓存思路）。
const iconFetchFailTTL = 10 * time.Minute

var (
	iconFailMu    sync.Mutex
	iconFailCache = map[string]time.Time{} // host -> 最近一次失败时间
)

func normIconHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

// checkFaviconCache 该站点已有图标文件时直接返回可访问路径（不发任何网络请求）
func checkFaviconCache(cfg *config.Config, host string) string {
	if cfg == nil {
		return ""
	}
	host = normIconHost(host)
	if host == "" {
		return ""
	}
	md5h := md5Hex(host)
	dir := filepath.Join(cfg.UploadDir, "favicon")
	for _, ext := range faviconExts {
		p := filepath.Join(dir, md5h+ext)
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Size() < 50 {
			continue
		}
		return "/frontend/uploads/favicon/" + md5h + ext
	}
	return ""
}

func iconFetchFailedRecently(host string) bool {
	host = normIconHost(host)
	if host == "" {
		return false
	}
	iconFailMu.Lock()
	defer iconFailMu.Unlock()
	at, ok := iconFailCache[host]
	if !ok {
		return false
	}
	if time.Since(at) < iconFetchFailTTL {
		return true
	}
	delete(iconFailCache, host)
	return false
}

func markIconFetchFailed(host string) {
	host = normIconHost(host)
	if host == "" {
		return
	}
	now := time.Now()
	iconFailMu.Lock()
	defer iconFailMu.Unlock()
	if len(iconFailCache) > 2000 {
		for h, at := range iconFailCache {
			if now.Sub(at) > iconFetchFailTTL {
				delete(iconFailCache, h)
			}
		}
	}
	iconFailCache[host] = now
}

func clearIconFetchFailed(host string) {
	host = normIconHost(host)
	if host == "" {
		return
	}
	iconFailMu.Lock()
	defer iconFailMu.Unlock()
	delete(iconFailCache, host)
}

// hostOfURL 取 URL 的主机名（忽略端口），用于图标按站点去重
func hostOfURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}
