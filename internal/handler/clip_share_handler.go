package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
)

// ClipShareHandler 处理 Android PWA Web Share Target 的表单 POST。
// 分享面板无法携带自定义请求头（CSRF），因此改用同源校验 + 登录态。
type ClipShareHandler struct {
	clips *ClipHandler
}

func NewClipShareHandler(clips *ClipHandler) *ClipShareHandler {
	return &ClipShareHandler{clips: clips}
}

// Share POST /clip-share
func (h *ClipShareHandler) Share(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/clip", http.StatusSeeOther)
		return
	}
	if !sameOriginRequest(r) {
		http.Error(w, "跨站请求已拒绝", http.StatusForbidden)
		return
	}

	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		http.Redirect(w, r, "/login.html?redirect=%2Fclip", http.StatusSeeOther)
		return
	}
	if sess.Role != "admin" && sess.Role != "editor" {
		clipRedirect(w, r, url.Values{"status": {"error"}, "reason": {"forbidden"}})
		return
	}
	if !clipEnabled() {
		clipRedirect(w, r, url.Values{"status": {"error"}, "reason": {"disabled"}})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		clipRedirect(w, r, url.Values{"status": {"error"}, "reason": {"bad-request"}})
		return
	}

	rawURL := strings.TrimSpace(r.FormValue("url"))
	text := strings.TrimSpace(r.FormValue("text"))
	title := strings.TrimSpace(r.FormValue("title"))
	target := rawURL
	if target == "" {
		target = extractURLFromText(text)
	}
	if target == "" {
		target = extractURLFromText(title)
	}
	if target == "" {
		clipRedirect(w, r, url.Values{"status": {"error"}, "reason": {"no-url"}})
		return
	}

	var username, name string
	db.DB.QueryRow("SELECT username, name FROM users WHERE id = ?", sess.UID).Scan(&username, &name)
	p := &clipPrincipal{UID: sess.UID, Username: username, Name: name, Role: sess.Role, ViaToken: false,
		Scopes: map[string]bool{"clip:write": true, "clip:read": true, "groups:read": true}}

	res, code, msg := h.clips.createClip(w, r, p, clipInput{URL: target, Title: title, Source: "android-share"})
	if code != 0 || res == nil {
		clipRedirect(w, r, url.Values{"status": {"error"}, "reason": {msg}})
		return
	}
	status := "saved"
	if res.Duplicate {
		status = "duplicate"
	}
	clipRedirect(w, r, url.Values{
		"status": {status},
		"item":   {strconv.Itoa(res.Item.ID)},
		"title":  {res.Item.Title},
		"group":  {res.Group},
		"url":    {target},
	})
}

func clipRedirect(w http.ResponseWriter, r *http.Request, params url.Values) {
	http.Redirect(w, r, "/clip?"+params.Encode(), http.StatusSeeOther)
}

// sameOriginRequest 校验 Origin / Sec-Fetch-Site，阻断第三方页面伪造分享表单
func sameOriginRequest(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	scheme := "http"
	if auth.IsHTTPS(r) {
		scheme = "https"
	}
	expected := scheme + "://" + r.Host
	return strings.EqualFold(strings.TrimRight(origin, "/"), expected)
}
