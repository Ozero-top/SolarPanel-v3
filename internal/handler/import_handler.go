package handler

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"solarpanel/internal/auth"
)

// ImportHandler 浏览器书签导入
type ImportHandler struct{}

func NewImportHandler() *ImportHandler { return &ImportHandler{} }

func (h *ImportHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 401, "未登录")
		return
	}
	if sess.Role == "viewer" || sess.Role == "guest" {
		Err(w, http.StatusForbidden, 403, "只读账号不可导入书签")
		return
	}
	action := r.URL.Query().Get("action")
	if action == "" {
		action = r.FormValue("action")
	}
	switch action {
	case "bookmarks":
		h.Bookmarks(w, r)
	default:
		Err(w, http.StatusBadRequest, 1, "unknown action: "+action+"，请传 action=bookmarks 上传文件")
	}
}

type bookmarkEntry struct {
	GroupTitle string
	Title      string
	URL        string
	Icon       string
}

func (h *ImportHandler) Bookmarks(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		Err(w, http.StatusBadRequest, 1, "未收到文件")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		Err(w, http.StatusBadRequest, 1, "读取文件失败")
		return
	}

	entries := parseBookmarksHTML(string(data))
	if len(entries) == 0 {
		OK(w, map[string]any{"groups": []any{}, "total": 0})
		return
	}

	type group struct {
		Title string
		Count int
	}
	groupMap := make(map[string]int)
	var groupOrder []string

	for _, e := range entries {
		gTitle := e.GroupTitle
		if gTitle == "" {
			gTitle = "导入书签"
		}
		if _, ok := groupMap[gTitle]; !ok {
			groupMap[gTitle] = 0
			groupOrder = append(groupOrder, gTitle)
		}
		groupMap[gTitle]++
	}

	var result []group
	var total int
	for _, gTitle := range groupOrder {
		cnt := groupMap[gTitle]
		if cnt > 0 {
			result = append(result, group{Title: gTitle, Count: cnt})
			total += cnt
		}
	}

	OK(w, map[string]any{"groups": result, "total": total})
}

func parseBookmarksHTML(htmlContent string) []bookmarkEntry {
	var entries []bookmarkEntry
	s := strings.TrimLeft(htmlContent, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	allowedProto := regexp.MustCompile(`^https?://`)
	h3Re := regexp.MustCompile(`(?i)<h3[^>]*>(.*?)</h3>`)
	aRe := regexp.MustCompile(`(?i)<a\s+[^>]*?href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)

	h3Matches := h3Re.FindAllStringIndex(s, -1)
	topH3End := 0
	if len(h3Matches) > 0 {
		topH3End = h3Matches[0][0]
	}
	for _, m := range aRe.FindAllStringSubmatchIndex(s, -1) {
		if m[0] < topH3End {
			urlRaw := s[m[2]:m[3]]
			titleRaw := s[m[4]:m[5]]
			urlDecoded, err := url.QueryUnescape(html.UnescapeString(urlRaw))
			if err != nil {
				urlDecoded = html.UnescapeString(urlRaw)
			}
			title := strings.TrimSpace(html.UnescapeString(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(titleRaw, "")))
			if allowedProto.MatchString(urlDecoded) && title != "" {
				entries = append(entries, bookmarkEntry{GroupTitle: "", Title: title, URL: urlDecoded})
			}
		}
	}

	for i, h3m := range h3Matches {
		groupTitle := strings.TrimSpace(html.UnescapeString(s[h3m[2]:h3m[3]]))
		if groupTitle == "" {
			groupTitle = "导入分组"
		}
		segStart := h3m[1]
		segEnd := len(s)
		if i+1 < len(h3Matches) {
			segEnd = h3Matches[i+1][0]
		}
		seg := s[segStart:segEnd]

		for _, am := range aRe.FindAllStringSubmatch(seg, -1) {
			urlDecoded, err := url.QueryUnescape(html.UnescapeString(am[1]))
			if err != nil {
				urlDecoded = html.UnescapeString(am[1])
			}
			title := strings.TrimSpace(html.UnescapeString(
				regexp.MustCompile(`<[^>]+>`).ReplaceAllString(am[2], ""),
			))
			if allowedProto.MatchString(urlDecoded) && title != "" {
				entries = append(entries, bookmarkEntry{GroupTitle: groupTitle, Title: title, URL: urlDecoded})
			}
		}
	}
	return entries
}
