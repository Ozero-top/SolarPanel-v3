package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"solarpanel/internal/config"
)

const (
	clipFetchMaxBytes    = 5 * 1024 * 1024
	clipFetchMaxPDFBytes = 15 * 1024 * 1024
	clipFetchTimeout     = 10 * time.Second
	clipFetchMaxRedirect = 3
)

// clipPage 服务端抓取到的页面数据：P0 只用元数据，P1 追加整页 HTML 供正文提取，
// P2 支持 PDF：命中 PDF 时把原始字节放进 PDFData，由提取阶段转成文本快照。
type clipPage struct {
	FinalURL   *url.URL
	Title      string
	Desc       string
	Canonical  string
	CoverURL   string
	IconCands  []string
	HTML       string
	PDFData    []byte
	IsPDF      bool
	HTTPStatus int
	FetchError string
}

// fetchClipPage 安全抓取目标页面：限制协议 / 大小 / 超时 / 重定向跳数，
// 并在每次重定向与建连时校验解析后的真实 IP（防 SSRF 与 DNS 重绑定）。
func fetchClipPage(rawURL string) *clipPage {
	out := &clipPage{}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		out.FetchError = "URL 不合法"
		return out
	}
	if isPrivateHost(u.Hostname()) {
		out.FetchError = "目标地址位于内网，已拒绝抓取"
		return out
	}

	client := &http.Client{Timeout: clipFetchTimeout, Transport: safeHTTPTransport}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= clipFetchMaxRedirect {
			return fmt.Errorf("重定向次数过多")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("重定向到非 HTTP 协议")
		}
		if isPrivateHost(req.URL.Hostname()) {
			return fmt.Errorf("重定向到内网地址")
		}
		return nil
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		out.FetchError = "构造请求失败"
		return out
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36 SolarPanel-Clipper/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		out.FetchError = "无法访问目标站点：" + err.Error()
		return out
	}
	defer resp.Body.Close()
	out.FinalURL = resp.Request.URL

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.FetchError = fmt.Sprintf("目标站点返回 HTTP %d", resp.StatusCode)
		return out
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	// PDF：下载原始字节，交给 PDF 文本提取；不再要求 HTML
	isPDF := strings.Contains(ct, "pdf")
	if !isPDF && out.FinalURL != nil {
		isPDF = strings.HasSuffix(strings.ToLower(out.FinalURL.Path), ".pdf")
	}
	if isPDF {
		if resp.ContentLength > clipFetchMaxPDFBytes {
			out.FetchError = "PDF 超过 15MB"
			return out
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, clipFetchMaxPDFBytes+1))
		if err != nil {
			out.FetchError = "读取 PDF 失败"
			return out
		}
		if len(data) > clipFetchMaxPDFBytes {
			out.FetchError = "PDF 超过 15MB"
			return out
		}
		out.IsPDF = true
		out.PDFData = data
		out.HTTPStatus = resp.StatusCode
		out.Title = pdfTitleFromURL(out.FinalURL)
		return out
	}
	if ct != "" && !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
		out.FetchError = "目标不是 HTML 页面"
		return out
	}
	if resp.ContentLength > clipFetchMaxBytes {
		out.FetchError = "页面超过 5MB"
		return out
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, clipFetchMaxBytes+1))
	if err != nil {
		out.FetchError = "读取页面失败"
		return out
	}
	if len(body) > clipFetchMaxBytes {
		out.FetchError = "页面超过 5MB"
		return out
	}
	html := string(body)
	out.HTML = html
	out.HTTPStatus = resp.StatusCode

	out.Title, out.Desc = extractMeta(html)
	out.CoverURL = absolutizeURL(firstMetaContent(html, reOGImage, reOGImage2, reTwImage, reTwImage2), out.FinalURL)
	out.Canonical = extractCanonical(html, out.FinalURL)
	out.IconCands = extractIconLinks(html, out.FinalURL)
	return out
}

// pdfTitleFromURL 从 PDF 地址推导标题（文件名去扩展名后把连字符转空格）
func pdfTitleFromURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	name := filepath.Base(u.Path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	name = strings.NewReplacer("-", " ", "_", " ").Replace(strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" {
		return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	}
	return trimRunes(name, 200)
}

// extractCanonical 读取 <link rel="canonical">，只接受与最终页面同源的结果，
// 避免站点把 canonical 指到第三方域名导致查重键失控。
func extractCanonical(html string, base *url.URL) string {
	if base == nil {
		return ""
	}
	for _, m := range reLinkIcon.FindAllStringSubmatch(html, -1) {
		attrs := m[1]
		relMatch := reLinkRel.FindStringSubmatch(attrs)
		if relMatch == nil || !strings.Contains(strings.ToLower(relMatch[1]), "canonical") {
			continue
		}
		hrefMatch := reLinkHref.FindStringSubmatch(attrs)
		if hrefMatch == nil {
			continue
		}
		abs := absolutizeURL(hrefMatch[1], base)
		if abs == "" {
			continue
		}
		cu, err := url.Parse(abs)
		if err != nil || !strings.EqualFold(cu.Scheme, base.Scheme) || !strings.EqualFold(cu.Host, base.Host) {
			continue
		}
		return abs
	}
	return ""
}

// clipIconCandidates 组装图标候选：页面声明的图标 → /favicon.ico → 公共图标源
func clipIconCandidates(page *clipPage, host string) []string {
	cands := append([]string{}, page.IconCands...)
	scheme := "https"
	if page.FinalURL != nil {
		scheme = page.FinalURL.Scheme
	}
	ico := scheme + "://" + host + "/favicon.ico"
	hasIco := false
	for _, c := range cands {
		if strings.Contains(c, "/favicon.ico") {
			hasIco = true
			break
		}
	}
	if !hasIco {
		cands = append(cands, ico)
	}
	for _, tmpl := range faviconFallbackServices {
		cands = append(cands, fmt.Sprintf(tmpl, host))
	}
	return cands
}

// saveClipCover 下载 og:image 封面并落盘 uploads/clips，返回可访问路径。
// 封面只接受位图，拒绝 SVG/ICO，避免把可执行内容当图片存储。
func saveClipCover(cfg *config.Config, urlKey, coverURL string) string {
	if coverURL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	data, ct, err := fetchIconBytes(ctx, coverURL, 6*time.Second)
	if err != nil || len(data) < 200 {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(ct), "image/") {
		return ""
	}
	ext := detectImageExt(data)
	if ext == "" {
		ext = extractExtFromCT(ct)
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
	default:
		return ""
	}
	dir := filepath.Join(cfg.UploadDir, "clips")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ""
	}
	name := md5Hex(urlKey) + ext
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		return ""
	}
	return "/frontend/uploads/clips/" + name
}
