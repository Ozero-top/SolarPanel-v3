package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
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

// downloadFavicon 下载图标并保存到上传目录的日期子目录，返回前端可访问的静态路径。
func downloadFavicon(cfg *config.Config, iconURL string) (string, error) {
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

	now := time.Now()
	rel := now.Format("2006/01/02")
	dir := filepath.Join(cfg.UploadDir, now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	// 文件名 = md5(图标地址 + 纳秒时间戳) + 原扩展名
	name := md5Hex(iconURL+strconv.FormatInt(now.UnixNano(), 10)) + path.Ext(u.Path)
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, data, 0644); err != nil {
		return "", err
	}
	return "/frontend/uploads/" + rel + "/" + name, nil
}
