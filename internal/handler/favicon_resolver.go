package handler

// 站点图标解析：候选收集（HTML / manifest / 默认路径）→ 并发抓取 → 质量优先选优。
// 原先「谁先返回就用谁」会让 16px ico 抢占高清 apple-touch-icon，这里改为按声明质量 + 实际像素打分。

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"solarpanel/internal/config"
)

// iconCandidate 图标候选：URL + 声明质量分（越大越优）
// RasterOnly 为 true 时只接受位图：公共图标服务对未知域名常返回「字母占位 SVG」，不适合当真实 logo
type iconCandidate struct {
	URL        string
	Score      int
	RasterOnly bool
}

// iconFetchResult 单个候选的抓取结果
type iconFetchResult struct {
	cand iconCandidate
	data []byte
	ct   string
	ext  string
	w, h int
}

var (
	reLinkType     = regexp.MustCompile(`(?is)\btype\s*=\s*["']([^"']+)["']`)
	reManifestIcon = regexp.MustCompile(`(?is)<link\b([^>]*)>`)
)

// extractIconCandidates 解析页面 <link rel=icon/apple-touch-icon>，按声明质量排序。
// 不包含 manifest（需额外网络请求）与默认路径探测，由调用方拼接。
func extractIconCandidates(html string, base *url.URL) []iconCandidate {
	var cands []iconCandidate
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
		if tm := reLinkType.FindStringSubmatch(attrs); tm != nil {
			t := strings.ToLower(tm[1])
			if strings.Contains(t, "svg") {
				score += 25 // SVG 矢量优先，缩放不会糊
			}
		}
		if strings.HasSuffix(strings.ToLower(href), ".svg") {
			score += 20
		}
		if sm := reLinkSizes.FindStringSubmatch(attrs); sm != nil {
			for _, part := range strings.Split(sm[1], " ") {
				part = strings.ToLower(strings.TrimSpace(part))
				if part == "any" {
					score += 35
					continue
				}
				var w, h int
				if _, err := fmt.Sscanf(part, "%dx%d", &w, &h); err == nil && w > 0 {
					if w >= 192 {
						score += 35
					} else if w >= 96 {
						score += 30
					} else if w >= 48 {
						score += 15
					} else {
						score += w / 16
					}
				}
			}
		}
		cands = append(cands, iconCandidate{URL: href, Score: score})
	}
	return cands
}

// manifestIconCandidates 抓取页面声明的 web app manifest，取其中的 icons（通常是 192/512 的正牌 logo）
func manifestIconCandidates(html string, base *url.URL) []iconCandidate {
	var manifestURL string
	for _, m := range reManifestIcon.FindAllStringSubmatch(html, -1) {
		attrs := m[1]
		relMatch := reLinkRel.FindStringSubmatch(attrs)
		if relMatch == nil || !strings.Contains(strings.ToLower(relMatch[1]), "manifest") {
			continue
		}
		hrefMatch := reLinkHref.FindStringSubmatch(attrs)
		if hrefMatch == nil {
			continue
		}
		manifestURL = absolutizeURL(hrefMatch[1], base)
		if manifestURL != "" {
			break
		}
	}
	if manifestURL == "" {
		return nil
	}
	data, err := fetchSmallText(manifestURL, 512*1024, 4*time.Second)
	if err != nil {
		return nil
	}
	var mf struct {
		Icons []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if json.Unmarshal(data, &mf) != nil {
		return nil
	}
	var out []iconCandidate
	for _, ic := range mf.Icons {
		if strings.TrimSpace(ic.Src) == "" {
			continue
		}
		if strings.Contains(strings.ToLower(ic.Purpose), "monochrome") {
			continue // 单色遮罩图，不适合直接作为站点图标展示
		}
		u := absolutizeURL(ic.Src, base)
		if u == "" {
			continue
		}
		score := 40
		biggest := 0
		for _, part := range strings.Split(ic.Sizes, " ") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "any" {
				score = 65
				continue
			}
			var w, h int
			if _, err := fmt.Sscanf(part, "%dx%d", &w, &h); err == nil && w > biggest {
				biggest = w
			}
		}
		switch {
		case biggest >= 512:
			score = 62
		case biggest >= 192:
			score = 58
		case biggest >= 180:
			score = 55
		case biggest >= 96:
			score = 45
		case biggest >= 48:
			score = 30
		}
		out = append(out, iconCandidate{URL: u, Score: score})
	}
	return out
}

// rootIconCandidates 站点没声明图标时的常见默认路径
func rootIconCandidates(base *url.URL) []iconCandidate {
	origin := base.Scheme + "://" + base.Host
	return []iconCandidate{
		{URL: origin + "/favicon.svg", Score: 61},
		{URL: origin + "/apple-touch-icon.png", Score: 56},
		{URL: origin + "/apple-touch-icon-precomposed.png", Score: 52},
		{URL: origin + "/favicon.png", Score: 26},
	}
}

// fetchSmallText 抓取小型文本资源（manifest 等），限制大小与超时
func fetchSmallText(rawURL string, limit int64, timeout time.Duration) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || isPrivateHost(u.Hostname()) {
		return nil, fmt.Errorf("invalid manifest url")
	}
	client := newSafeHTTPClient(timeout)
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// imagePixelSize 读取常见图片格式的真实像素尺寸；无法识别时返回 0,0
func imagePixelSize(data []byte, ext string) (int, int) {
	switch ext {
	case ".png":
		if len(data) >= 24 && data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
			return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24]))
		}
	case ".ico":
		if len(data) >= 8 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x01 && data[3] == 0x00 {
			w, h := int(data[6]), int(data[7])
			if w == 0 {
				w = 256
			}
			if h == 0 {
				h = 256
			}
			return w, h
		}
	case ".gif":
		if len(data) >= 10 && data[0] == 0x47 && data[1] == 0x49 && data[2] == 0x46 {
			return int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
		}
	case ".jpg":
		// 跳过 JFIF 头，找 SOF0/SOF1/SOF2 段读宽高
		for i := 2; i+9 < len(data); {
			if data[i] != 0xFF {
				i++
				continue
			}
			marker := data[i+1]
			if marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
				return int(binary.BigEndian.Uint16(data[i+7 : i+9])), int(binary.BigEndian.Uint16(data[i+5 : i+7]))
			}
			segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
			if segLen < 2 {
				break
			}
			i += 2 + segLen
		}
	case ".svg":
		return 512, 512 // 矢量图按大图计分
	}
	return 0, 0
}

// iconQualityScore 候选综合得分：声明质量优先，其次真实像素，最后文件体积（细节更多）
func iconQualityScore(r iconFetchResult) int {
	s := r.cand.Score * 100
	if r.ext == ".svg" {
		s += 90
	} else {
		px := r.w
		if r.h > 0 && r.h < px {
			px = r.h
		}
		switch {
		case px >= 180:
			s += 70
		case px >= 96:
			s += 55
		case px >= 64:
			s += 40
		case px >= 32:
			s += 25
		default:
			s += 8
		}
	}
	if len(r.data) > 8000 {
		s += 5
	}
	return s
}

// fetchBestIcon 并发抓取候选，质量最优者落盘；返回本地相对路径，失败返回空
func fetchBestIcon(cfg *config.Config, host string, cands []iconCandidate) string {
	seen := make(map[string]bool, len(cands))
	list := make([]iconCandidate, 0, len(cands))
	for _, c := range cands {
		u := strings.TrimSpace(c.URL)
		if u == "" || seen[u] || isPrivateHostMustParse(u) {
			continue
		}
		seen[u] = true
		list = append(list, c)
		if len(list) >= 12 {
			break
		}
	}
	if len(list) == 0 {
		return ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan iconFetchResult, len(list))
	for _, c := range list {
		go func(c iconCandidate) {
			data, ct, err := fetchIconBytes(ctx, c.URL, 5*time.Second)
			if err != nil {
				ch <- iconFetchResult{cand: c}
				return
			}
			ch <- iconFetchResult{cand: c, data: data, ct: ct}
		}(c)
	}

	// 首个可用结果到达后再等 600ms，收集同批更优候选；全部失败则等到最后一个返回
	var best iconFetchResult
	bestScore := -1
	var graceDeadline time.Time
	remaining := len(list)
	for remaining > 0 {
		var r iconFetchResult
		if graceDeadline.IsZero() {
			r = <-ch
		} else {
			wait := time.Until(graceDeadline)
			if wait <= 0 {
				break
			}
			select {
			case r = <-ch:
			case <-time.After(wait):
				remaining = 0
				continue
			}
		}
		remaining--

		if len(r.data) < 50 {
			continue
		}
		ext := detectImageExt(r.data)
		if ext == "" {
			ext = extractExtFromCT(r.ct)
		}
		if ext == "" {
			ext = extFromURL(r.cand.URL)
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
		if r.cand.RasterOnly && ext == ".svg" {
			continue
		}
		// 有些站点把图标地址返回成 HTML（反爬页/错误页），按 URL 后缀当成图片落盘会导致前端永远裂图
		if detectImageExt(r.data) == "" && !strings.HasPrefix(strings.ToLower(r.ct), "image/") {
			continue
		}
		r.ext = ext
		r.w, r.h = imagePixelSize(r.data, ext)
		if r.w > 0 && r.w < 16 {
			continue // 16px 以下的噪点图不参与评选
		}
		if s := iconQualityScore(r); s > bestScore {
			best, bestScore = r, s
		}
		if graceDeadline.IsZero() {
			graceDeadline = time.Now().Add(600 * time.Millisecond)
		}
	}

	cancel()
	if bestScore < 0 {
		return ""
	}
	if saved := saveFavicon(cfg, host, best.ext, best.data); saved != "" {
		log.Printf("[favicon] host=%s — saved via %s (%d bytes, %dx%d)", host, best.cand.URL, len(best.data), best.w, best.h)
		return saved
	}
	return ""
}
