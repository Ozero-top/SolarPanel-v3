package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName       = "sp_session"
	csrfCookieName          = "csrf_token"
	trustedDeviceCookie     = "trusted_device"
	trustedDeviceMaxAge     = 86400 * 30
	trustedDeviceMaxPerUser = 10
)

type AuthHandler struct {
	Store *auth.Store
	cfg   *config.Config

	rateMu       sync.Mutex
	rateAttempts map[string][]time.Time
}

// dummyPasswordHash 用户不存在时也执行一次 bcrypt 比对，避免通过响应耗时枚举用户名
var dummyPasswordHash, _ = bcrypt.GenerateFromPassword([]byte("solarpanel-dummy-password"), bcrypt.DefaultCost)

func NewAuthHandler(store *auth.Store, cfg *config.Config) *AuthHandler {
	h := &AuthHandler{
		Store:        store,
		cfg:          cfg,
		rateAttempts: make(map[string][]time.Time),
	}
	go h.rateGC()
	return h
}

func (h *AuthHandler) rateGC() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		h.rateMu.Lock()
		cutoff := time.Now().Add(-60 * time.Second)
		for ip, arr := range h.rateAttempts {
			i := 0
			for i < len(arr) && arr[i].Before(cutoff) {
				i++
			}
			if i == len(arr) {
				delete(h.rateAttempts, ip)
			} else {
				h.rateAttempts[ip] = arr[i:]
			}
		}
		h.rateMu.Unlock()
	}
}

func (h *AuthHandler) checkRate(ip string) bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()
	cutoff := time.Now().Add(-60 * time.Second)
	arr := h.rateAttempts[ip]
	i := 0
	for i < len(arr) && arr[i].Before(cutoff) {
		i++
	}
	arr = arr[i:]
	if len(arr) >= 8 {
		h.rateAttempts[ip] = arr
		return false
	}
	arr = append(arr, time.Now())
	h.rateAttempts[ip] = arr
	return true
}

// ===== Me / 当前用户 =====

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录或登录已过期")
		return
	}
	if sess.UID == 0 {
		OK(w, map[string]any{
			"id": 0, "username": "guest", "name": "访客",
			"role": "guest", "csrf": sess.CSRFToken, "has_2fa": false,
		})
		return
	}
	var u UserRow
	err := db.DB.QueryRow("SELECT id, username, name, status, role, totp_secret FROM users WHERE id=?", sess.UID).Scan(
		&u.ID, &u.Username, &u.Name, &u.Status, &u.Role, &u.TOTPSecret)
	if err != nil {
		Err(w, http.StatusUnauthorized, 1, "用户不存在")
		return
	}
	OK(w, map[string]any{
		"id": u.ID, "username": u.Username, "name": u.Name,
		"role": u.Role, "csrf": sess.CSRFToken, "has_2fa": u.TOTPSecret != "",
	})
}

// ===== Login =====

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if body.Username == "" || body.Password == "" {
		Fail(w, "用户名或密码不能为空")
		return
	}

	ip := auth.ClientIP(r)
	if !h.checkRate(ip) {
		Fail(w, "尝试过于频繁，请 60 秒后再试")
		return
	}

	var u UserRow
	err := db.DB.QueryRow("SELECT id, username, name, password, status, role, totp_secret FROM users WHERE username=?", body.Username).Scan(
		&u.ID, &u.Username, &u.Name, &u.Password, &u.Status, &u.Role, &u.TOTPSecret)
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(body.Password))
		auth.AuditLog(w, r, "auth.login", body.Username, "fail", body.Username, "", "用户不存在或密码错误")
		Fail(w, "用户名或密码错误")
		return
	}
	if u.Status != 1 {
		auth.AuditLog(w, r, "auth.login", u.Username, "fail", u.Username, u.Role, "账号被禁用")
		Fail(w, "账号已被禁用")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(body.Password)); err != nil {
		auth.AuditLog(w, r, "auth.login", u.Username, "fail", u.Username, u.Role, "密码错误")
		Fail(w, "用户名或密码错误")
		return
	}

	if u.TOTPSecret != "" && u.Role == "admin" && body.Code == "" {
		if verifyTrustedDevice(uint(u.ID), r) {
			sid, csrf := h.Store.CreateSession(uint(u.ID), u.Role)
			auth.SetCookies(w, sid, csrf, sessionCookieName, csrfCookieName, auth.IsHTTPS(r))
			auth.AuditLog(w, r, "auth.login", u.Username, "success", u.Username, u.Role, "trusted_device_bypass")
			OK(w, map[string]any{"id": u.ID, "username": u.Username, "name": u.Name, "role": u.Role})
			return
		}
		OK(w, map[string]any{"need_2fa": true, "uid": u.ID})
		return
	}
	if u.TOTPSecret != "" && u.Role == "admin" && body.Code != "" {
		if !auth.TOTPVerify(u.TOTPSecret, body.Code) {
			auth.AuditLog(w, r, "auth.login.2fa", u.Username, "fail", u.Username, u.Role, "2FA 动态码错误")
			Fail(w, "2FA 动态码错误")
			return
		}
	}

	sid, csrf := h.Store.CreateSession(uint(u.ID), u.Role)
	auth.SetCookies(w, sid, csrf, sessionCookieName, csrfCookieName, auth.IsHTTPS(r))
	auth.AuditLog(w, r, "auth.login", u.Username, "success", u.Username, u.Role, "登录成功")
	OK(w, map[string]any{"id": u.ID, "username": u.Username, "name": u.Name, "role": u.Role})
}

func (h *AuthHandler) LoginWith2FA(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UID         uint   `json:"uid"`
		Code        string `json:"code"`
		TrustDevice int    `json:"trust_device"`
	}
	if err := BindJSON(r, &body); err != nil || body.UID == 0 || body.Code == "" {
		Fail(w, "参数错误")
		return
	}
	if !h.checkRate(auth.ClientIP(r)) {
		Fail(w, "尝试过于频繁，请 60 秒后再试")
		return
	}

	var u UserRow
	err := db.DB.QueryRow("SELECT id, username, name, password, status, role, totp_secret FROM users WHERE id=?", body.UID).Scan(
		&u.ID, &u.Username, &u.Name, &u.Password, &u.Status, &u.Role, &u.TOTPSecret)
	if err != nil {
		Fail(w, "用户不存在")
		return
	}
	if u.TOTPSecret == "" || u.Role != "admin" {
		Fail(w, "该账号未开启 2FA")
		return
	}
	if !auth.TOTPVerify(u.TOTPSecret, body.Code) {
		auth.AuditLog(w, r, "auth.login.2fa", u.Username, "fail", u.Username, u.Role, "2FA 动态码错误")
		Fail(w, "2FA 动态码错误")
		return
	}

	sid, csrf := h.Store.CreateSession(uint(u.ID), u.Role)
	auth.SetCookies(w, sid, csrf, sessionCookieName, csrfCookieName, auth.IsHTTPS(r))

	if body.TrustDevice == 1 {
		if err := saveTrustedDevice(uint(u.ID), r, w); err != nil {
			auth.AuditLog(w, r, "trusted_device.save", u.Username, "fail", u.Username, u.Role, err.Error())
		} else {
			auth.AuditLog(w, r, "trusted_device.save", u.Username, "success", u.Username, u.Role, "")
		}
	}
	auth.AuditLog(w, r, "auth.login", u.Username, "success", u.Username, u.Role, "2FA 登录成功")
	OK(w, map[string]any{"id": u.ID, "username": u.Username, "name": u.Name, "role": u.Role})
}

func (h *AuthHandler) GuestLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}

	ip := auth.ClientIP(r)
	if !h.checkRate(ip) {
		Fail(w, "尝试过于频繁，请 60 秒后再试")
		return
	}

	enabled := db.GetSetting("guest_access_enabled")
	if enabled != "1" {
		Fail(w, "访客访问密码未开启")
		return
	}
	hash := db.GetSetting("guest_password_hash")
	if hash == "" {
		Fail(w, "访客访问密码未设置")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)); err != nil {
		auth.AuditLog(w, r, "guest.login", "访客", "fail", "访客", "", "访客密码错误")
		Fail(w, "密码错误")
		return
	}

	sid, csrf := h.Store.CreateSession(0, "guest")
	auth.SetCookies(w, sid, csrf, sessionCookieName, csrfCookieName, auth.IsHTTPS(r))
	auth.AuditLog(w, r, "guest.login", "访客", "success", "访客", "guest", "访客密码通过")
	OK(w, map[string]any{"role": "guest", "msg": "访客密码通过"})
}

// ===== Logout =====

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	sid := auth.GetSessionIDFromRequest(r)
	if sid != "" {
		h.Store.DestroySession(sid)
	}
	auth.ClearCookies(w, sessionCookieName, csrfCookieName, auth.IsHTTPS(r))
	OK(w, nil)
}

// ===== 2FA =====

func (h *AuthHandler) TwoFASetup(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅 admin 角色可修改 2FA 设置")
		return
	}
	var u UserRow
	err := db.DB.QueryRow("SELECT id, username, name, password, status, role, totp_secret FROM users WHERE id=?", sess.UID).Scan(
		&u.ID, &u.Username, &u.Name, &u.Password, &u.Status, &u.Role, &u.TOTPSecret)
	if err != nil {
		Err(w, http.StatusNotFound, 1, "用户不存在")
		return
	}
	if u.TOTPSecret != "" {
		Fail(w, "2FA 已开启，请先关闭后再设置")
		return
	}
	secret, err := auth.TOTPSecretNew()
	if err != nil {
		Fail(w, "生成密钥失败")
		return
	}
	OK(w, map[string]any{
		"secret": secret,
		"url":    auth.TOTPProvisioningURL(secret, u.Username, "SolarPanel"),
	})
}

func (h *AuthHandler) TwoFAEnable(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅 admin 角色可修改 2FA 设置")
		return
	}
	if !auth.VerifyCSRF(r) {
		Err(w, http.StatusForbidden, 1, "CSRF 令牌无效或已过期，请刷新页面后重试")
		return
	}
	var body struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := BindJSON(r, &body); err != nil || body.Secret == "" || body.Code == "" {
		Fail(w, "参数错误")
		return
	}
	if !auth.TOTPVerify(body.Secret, body.Code) {
		Fail(w, "2FA 动态码错误，请确认密钥已正确绑定")
		return
	}
	db.DB.Exec("UPDATE users SET totp_secret=? WHERE id=?", body.Secret, sess.UID)
	auth.AuditLog(w, r, "2fa.enable", usernameByUID(sess.UID), "success", usernameByUID(sess.UID), sess.Role, "开启两步验证")
	OK(w, map[string]string{"msg": "2FA 已开启"})
}

func (h *AuthHandler) TwoFADisable(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅 admin 角色可修改 2FA 设置")
		return
	}
	if !auth.VerifyCSRF(r) {
		Err(w, http.StatusForbidden, 1, "CSRF 令牌无效或已过期，请刷新页面后重试")
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := BindJSON(r, &body); err != nil || body.Password == "" || body.Code == "" {
		Fail(w, "参数错误")
		return
	}
	var u UserRow
	err := db.DB.QueryRow("SELECT id, username, name, password, status, role, totp_secret FROM users WHERE id=?", sess.UID).Scan(
		&u.ID, &u.Username, &u.Name, &u.Password, &u.Status, &u.Role, &u.TOTPSecret)
	if err != nil {
		Err(w, http.StatusNotFound, 1, "用户不存在")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(body.Password)); err != nil {
		Fail(w, "密码错误")
		return
	}
	if u.TOTPSecret == "" {
		Fail(w, "2FA 未开启")
		return
	}
	if !auth.TOTPVerify(u.TOTPSecret, body.Code) {
		Fail(w, "2FA 动态码错误")
		return
	}
	db.DB.Exec("UPDATE users SET totp_secret='' WHERE id=?", sess.UID)
	auth.AuditLog(w, r, "2fa.disable", usernameByUID(sess.UID), "success", usernameByUID(sess.UID), sess.Role, "关闭两步验证")
	db.DB.Exec("DELETE FROM trusted_devices WHERE user_id=?", sess.UID)
	clearTrustedDeviceCookie(w, auth.IsHTTPS(r))
	OK(w, map[string]string{"msg": "2FA 已关闭"})
}

// ===== Trusted Device =====

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func clearTrustedDeviceCookie(w http.ResponseWriter, secure bool) {
	// codeql[go/cookie-secure-not-set] Secure 由 auth.IsHTTPS 按部署协议动态决定
	http.SetCookie(w, &http.Cookie{
		Name: trustedDeviceCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
		Secure: secure,
	})
}

func verifyTrustedDevice(uid uint, r *http.Request) bool {
	cookie, err := r.Cookie(trustedDeviceCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	hashHex := hex.EncodeToString(hash[:])

	nowStr := db.NowStr()
	var idVal int
	err = db.DB.QueryRow("SELECT id FROM trusted_devices WHERE token_hash=? AND user_id=? AND expires_at > ?", hashHex, uid, nowStr).Scan(&idVal)
	if err != nil {
		db.DB.Exec("DELETE FROM trusted_devices WHERE user_id=? AND expires_at < ?", uid, nowStr)
		return false
	}
	db.DB.Exec("UPDATE trusted_devices SET last_used_at=? WHERE token_hash=? AND user_id=?", db.NowStr(), hashHex, uid)
	return true
}

func saveTrustedDevice(uid uint, r *http.Request, w http.ResponseWriter) error {
	db.DB.Exec("DELETE FROM trusted_devices WHERE user_id=? AND expires_at < ?", uid, db.NowStr())

	var cnt int
	db.DB.QueryRow("SELECT COUNT(*) FROM trusted_devices WHERE user_id=?", uid).Scan(&cnt)
	if cnt >= trustedDeviceMaxPerUser {
		var oldestID int
		db.DB.QueryRow("SELECT id FROM trusted_devices WHERE user_id=? ORDER BY expires_at ASC LIMIT 1", uid).Scan(&oldestID)
		if oldestID > 0 {
			db.DB.Exec("DELETE FROM trusted_devices WHERE id=?", oldestID)
		}
	}

	token := randomToken(32)
	hash := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(hash[:])
	ip := auth.ClientIP(r)
	ipShort := ip
	if strings.Contains(ip, ":") {
		parts := strings.SplitN(ip, ":", 3)
		if len(parts) >= 2 {
			ipShort = parts[0] + ":" + parts[1] + ":****"
		}
	}
	ua := r.Header.Get("User-Agent")
	if len(ua) > 255 {
		ua = ua[:255]
	}
	deviceName := parseDeviceName(ua)
	expires := time.Now().Add(30 * 24 * time.Hour).Format("2006-01-02 15:04:05")

	db.DB.Exec("INSERT INTO trusted_devices (user_id, token_hash, device_name, ip_snippet, user_agent, created_at, last_used_at, expires_at) VALUES (?,?,?,?,?,?,?,?)",
		uid, hashHex, deviceName, ipShort, ua, db.NowStr(), db.NowStr(), expires)

	// codeql[go/cookie-secure-not-set] Secure 由 auth.IsHTTPS 按部署协议动态决定
	http.SetCookie(w, &http.Cookie{
		Name: trustedDeviceCookie, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: trustedDeviceMaxAge,
		Secure: auth.IsHTTPS(r),
	})
	return nil
}

func parseDeviceName(ua string) string {
	if ua == "" {
		return "未知设备"
	}
	if strings.Contains(ua, "iPhone") {
		return "iPhone / iOS"
	}
	if strings.Contains(ua, "Android") {
		return "Android"
	}
	if strings.Contains(ua, "iPad") {
		return "iPad / iOS"
	}
	var os, browser string
	switch {
	case strings.Contains(ua, "Windows NT 10"):
		os = "Windows 10/11"
	case strings.Contains(ua, "Windows NT 6.3"):
		os = "Windows 8.1"
	case strings.Contains(ua, "Mac OS X"):
		os = "macOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	default:
		os = "桌面"
	}
	switch {
	case strings.Contains(ua, "Chrome") && !strings.Contains(ua, "Edg"):
		browser = "Chrome"
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox"):
		browser = "Firefox"
	case strings.Contains(ua, "Safari"):
		browser = "Safari"
	default:
		browser = "浏览器"
	}
	return browser + " / " + os
}

func (h *AuthHandler) ListTrustedDevices(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	db.DB.Exec("DELETE FROM trusted_devices WHERE user_id=? AND expires_at < ?", sess.UID, db.NowStr())

	currentHash := ""
	if cookie, err := r.Cookie(trustedDeviceCookie); err == nil && cookie.Value != "" {
		hash := sha256.Sum256([]byte(cookie.Value))
		currentHash = hex.EncodeToString(hash[:])
	}

	rows, err := db.DB.Query("SELECT id, device_name, ip_snippet, last_used_at, expires_at, token_hash FROM trusted_devices WHERE user_id=? ORDER BY last_used_at DESC, created_at DESC", sess.UID)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取受信任设备失败")
		return
	}
	var list []struct {
		ID        int    `json:"id"`
		DeviceName string `json:"device_name"`
		IPSnippet  string `json:"ip_snippet"`
		LastUsedAt string `json:"last_used_at"`
		ExpiresAt  string `json:"expires_at"`
		IsCurrent  bool   `json:"is_current"`
	}
	for rows.Next() {
		var d struct {
			ID        int    `json:"id"`
			DeviceName string `json:"device_name"`
			IPSnippet  string `json:"ip_snippet"`
			LastUsedAt string `json:"last_used_at"`
			ExpiresAt  string `json:"expires_at"`
			IsCurrent  bool   `json:"is_current"`
		}
		var lu sql.NullString
		var tokenHash string
		rows.Scan(&d.ID, &d.DeviceName, &d.IPSnippet, &lu, &d.ExpiresAt, &tokenHash)
		if lu.Valid {
			d.LastUsedAt = lu.String
		}
		d.IsCurrent = currentHash != "" && tokenHash == currentHash
		list = append(list, d)
	}
	rows.Close()

	if list == nil {
		list = []struct {
			ID        int    `json:"id"`
			DeviceName string `json:"device_name"`
			IPSnippet  string `json:"ip_snippet"`
			LastUsedAt string `json:"last_used_at"`
			ExpiresAt  string `json:"expires_at"`
			IsCurrent  bool   `json:"is_current"`
		}{}
	}
	OK(w, list)
}

func (h *AuthHandler) DeleteTrustedDevice(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if !auth.VerifyCSRF(r) {
		Err(w, http.StatusForbidden, 1, "CSRF 令牌无效或已过期，请刷新页面后重试")
		return
	}
	var body struct {
		ID uint `json:"id"`
	}
	if err := BindJSON(r, &body); err != nil || body.ID == 0 {
		Fail(w, "参数错误")
		return
	}

	var tokenHash string
	err := db.DB.QueryRow("SELECT token_hash FROM trusted_devices WHERE id=? AND user_id=?", body.ID, sess.UID).Scan(&tokenHash)
	if err != nil {
		Err(w, http.StatusNotFound, 1, "设备不存在")
		return
	}

	currentHash := ""
	if cookie, err := r.Cookie(trustedDeviceCookie); err == nil && cookie.Value != "" {
		hash := sha256.Sum256([]byte(cookie.Value))
		currentHash = hex.EncodeToString(hash[:])
	}

	db.DB.Exec("DELETE FROM trusted_devices WHERE id=? AND user_id=?", body.ID, sess.UID)
	if currentHash != "" && currentHash == tokenHash {
		clearTrustedDeviceCookie(w, auth.IsHTTPS(r))
	}
	auth.AuditLog(w, r, "trusted_device.delete", usernameByUID(sess.UID), "success", usernameByUID(sess.UID), sess.Role, strconv.FormatUint(uint64(body.ID), 10))
	OK(w, nil)
}

func (h *AuthHandler) RevokeAllTrustedDevices(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if !auth.VerifyCSRF(r) {
		Err(w, http.StatusForbidden, 1, "CSRF 令牌无效或已过期，请刷新页面后重试")
		return
	}
	db.DB.Exec("DELETE FROM trusted_devices WHERE user_id=?", sess.UID)
	clearTrustedDeviceCookie(w, auth.IsHTTPS(r))
	auth.AuditLog(w, r, "trusted_device.revoke_all", usernameByUID(sess.UID), "success", usernameByUID(sess.UID), sess.Role, "")
	OK(w, nil)
}

// ===== Dispatch =====

func (h *AuthHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	action := r.URL.Query().Get("action")

	if method == "POST" {
		switch action {
		case "login":
			h.Login(w, r)
		case "login_2fa":
			h.LoginWith2FA(w, r)
		case "guest_login":
			h.GuestLogin(w, r)
		case "logout":
			h.Logout(w, r)
		case "2fa_setup":
			h.TwoFASetup(w, r)
		case "2fa_enable":
			h.TwoFAEnable(w, r)
		case "2fa_disable":
			h.TwoFADisable(w, r)
		case "delete_trusted_device":
			h.DeleteTrustedDevice(w, r)
		case "revoke_all_trusted_devices":
			h.RevokeAllTrustedDevices(w, r)
		default:
			Err(w, http.StatusBadRequest, 1, "unknown action: "+action)
		}
		return
	}
	// GET
	switch action {
	case "list_trusted_devices":
		h.ListTrustedDevices(w, r)
	default:
		h.Me(w, r)
	}
}

