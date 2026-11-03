package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"solarpanel/internal/config"
)

// Sun-Panel 同款站点图标链路：
//   1. GET 站点页面，解析 <link rel="...icon..."> 得到候选列表（只解析，不下载）；
//   2. 下载用户选中的图标时先 HEAD 校验，超过 1MB 直接失败，再 GET 原图落盘。
// 与 Sun-Panel 一致：原图直存、不做格式转换；但客户端必须设超时，
// 否则慢站会长期占住后台迁移 worker（Sun-Panel 原始实现无超时，是已知缺陷）。

const faviconMaxBytes = 0x100000 // 1MB

// siteFaviconTimeout 单次页面/图标请求的超时
const siteFaviconTimeout = 10 * time.Second

// siteFaviconClient 带超时的 http.Client
var siteFaviconClient = &http.Client{Timeout: siteFaviconTimeout}

// siteFaviconCandidates 返回站点的图标候选地址。
func siteFaviconCandidates(rawURL string) ([]string, error) {
	resp, err := siteFaviconClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("站点返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL 解析失败")
	}
	// 跟随重定向后以最终地址补全相对路径
	if resp.Request != nil && resp.Request.URL != nil {
		base = resp.Request.URL
	}

	list := extractFaviconLinks(string(body), base)
	if len(list) == 0 {
		// 没有候选时回退到站点根目录的 favicon.ico
		list = []string{base.Scheme + "://" + base.Host + "/favicon.ico"}
	}
	return list, nil
}

// extractFaviconLinks 遍历页面 <link> 节点，rel 中包含 "icon"（大小写敏感）且 href 非空即作为候选。
func extractFaviconLinks(pageHTML string, base *url.URL) []string {
	doc, err := html.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return nil
	}
	var list []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "link") {
			var rel, href string
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = a.Val
				case "href":
					href = a.Val
				}
			}
			if strings.Contains(rel, "icon") && href != "" {
				if abs := resolveFaviconURL(href, base); abs != "" {
					list = append(list, abs)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return list
}

func resolveFaviconURL(href string, base *url.URL) string {
	href = strings.TrimSpace(htmlDecodeReplacer.Replace(href))
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

// hostOfURL 取 URL 的主机名（忽略端口），用于图标按站点去重
func hostOfURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

// downloadFavicon 下载用户选中的图标。
// 存储层沿用原有实现：按 host 去重、魔数校验、位图统一缩到 ≤64px 并转 WebP；
// 只替换“获取/选择”流程（Sun-Panel 的候选列表 + 手动点选）。
func downloadFavicon(cfg *config.Config, host, iconURL string) (string, error) {
	u, err := url.Parse(iconURL)
	if err != nil {
		return "", fmt.Errorf("图标地址解析失败")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("图标地址必须为 http/https")
	}

	// 先 HEAD，状态码必须为 200
	headResp, err := siteFaviconClient.Head(iconURL)
	if err != nil {
		return "", err
	}
	headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("图标 HEAD 状态码 %d", headResp.StatusCode)
	}
	if headResp.ContentLength > faviconMaxBytes {
		return "", fmt.Errorf("图标超过 1MB")
	}

	// 再 GET 原图
	getResp, err := siteFaviconClient.Get(iconURL)
	if err != nil {
		return "", err
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("图标 GET 状态码 %d", getResp.StatusCode)
	}
	if getResp.ContentLength > faviconMaxBytes {
		return "", fmt.Errorf("图标超过 1MB")
	}
	data, err := io.ReadAll(io.LimitReader(getResp.Body, faviconMaxBytes+1))
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
		ext = extractExtFromCT(getResp.Header.Get("Content-Type"))
	}
	if ext == "" {
		ext = extFromURL(iconURL)
	}
	saved := saveFavicon(cfg, host, ext, data)
	if saved == "" {
		return "", fmt.Errorf("图标内容无效或无法识别")
	}
	return saved, nil
}
