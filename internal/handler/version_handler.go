package handler

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/db"
)

// 版本检查：探测 GitHub 上的最新版本，结果缓存在本地，避免频繁访问外网。
// 优先读取 Release 页面的 302 跳转地址，失败再读 tag 的 Atom 订阅
// （两者都不占用 GitHub API 配额，避免匿名调用被限流）。
const (
	updateReleaseURL  = "https://github.com/Ozero-top/SolarPanel-v3/releases/latest"
	updateTagsAtomURL = "https://github.com/Ozero-top/SolarPanel-v3/tags.atom"
	updateReleasePage = "https://github.com/Ozero-top/SolarPanel-v3/releases"
	updateCacheKey    = "update_latest_version"
	updateCheckedKey  = "update_checked_at"
	updateDefaultHour = 6
	updateFailBackoff = 5 * time.Minute
)

// atomTitleRe 取 Atom 条目里的 <title>（tag 名）
var atomTitleRe = regexp.MustCompile(`<title>([^<]+)</title>`)

// VersionHandler 版本检查接口
type VersionHandler struct{}

// updateClient 只用于探测版本，超时短且不跟随跳转（需要读取 302 的 Location）
var updateClient = &http.Client{
	Timeout: 3 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var (
	updateMu        sync.Mutex
	updateFailUntil time.Time
)

// Get 返回当前版本与最新版本；离线或失败时只返回当前版本，不报错
func (VersionHandler) Get(w http.ResponseWriter, r *http.Request) {
	current := AppVersion
	resp := map[string]any{
		"current":     current,
		"latest":      "",
		"has_update":  false,
		"enabled":     db.GetSetting("update_check") != "0",
		"release_url": updateReleasePage,
	}
	if !resp["enabled"].(bool) {
		OK(w, resp)
		return
	}

	latest, checkedAt := cachedVersion()
	force := r.URL.Query().Get("refresh") == "1"
	if force || latest == "" || updateExpired(checkedAt) {
		if v, ok := refreshVersion(force); ok {
			latest, checkedAt = v, time.Now().Unix()
		}
	}
	if latest == "" {
		OK(w, resp)
		return
	}
	resp["latest"] = latest
	resp["has_update"] = compareVersion(latest, current) > 0
	resp["checked_at"] = checkedAt
	OK(w, resp)
}

// cachedVersion 读取本地缓存的版本与检查时间
func cachedVersion() (string, int64) {
	v := normalizeVersion(db.GetSetting(updateCacheKey))
	ts, _ := strconv.ParseInt(db.GetSetting(updateCheckedKey), 10, 64)
	return v, ts
}

// updateExpired 缓存是否已过期（间隔由设置项控制，默认 6 小时，范围 1~24）
func updateExpired(checkedAt int64) bool {
	if checkedAt <= 0 {
		return true
	}
	hours := updateDefaultHour
	if n, err := strconv.Atoi(db.GetSetting("update_check_interval")); err == nil {
		if n < 1 {
			n = 1
		} else if n > 24 {
			n = 24
		}
		hours = n
	}
	return time.Since(time.Unix(checkedAt, 0)) >= time.Duration(hours)*time.Hour
}

// refreshVersion 重新探测最新版本；同一时刻只允许一个探测，失败后 5 分钟内不再重试。
// force=true 用于后台「立即检查」，跳过缓存有效期。
func refreshVersion(force bool) (string, bool) {
	updateMu.Lock()
	defer updateMu.Unlock()

	// 非强制检查：等锁期间可能已被其它请求刷新
	if !force {
		if v, checkedAt := cachedVersion(); v != "" && !updateExpired(checkedAt) {
			return v, true
		}
	}
	if time.Now().Before(updateFailUntil) {
		return "", false
	}
	v := probeLatestVersion()
	if v == "" {
		updateFailUntil = time.Now().Add(updateFailBackoff)
		return "", false
	}
	db.SetSetting(updateCacheKey, v)
	db.SetSetting(updateCheckedKey, strconv.FormatInt(time.Now().Unix(), 10))
	updateFailUntil = time.Time{}
	return v, true
}

// probeLatestVersion 先读 Release 页面的跳转地址，失败再读 tag 的 Atom 订阅
func probeLatestVersion() string {
	if v := probeLatestByRedirect(); v != "" {
		return v
	}
	return probeLatestByAtom()
}

func probeLatestByRedirect() string {
	req, err := http.NewRequest(http.MethodGet, updateReleaseURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "SolarPanel-UpdateCheck")
	resp, err := updateClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	// 有 Release 时 GitHub 返回 302，Location 形如 .../releases/tag/v3.3.5
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return ""
	}
	loc := resp.Header.Get("Location")
	idx := strings.LastIndex(loc, "/")
	if idx < 0 {
		return ""
	}
	return normalizeVersion(loc[idx+1:])
}

// probeLatestByAtom 读 tag 的 Atom 订阅（公共源，无 API 配额限制），取最大版本号
func probeLatestByAtom() string {
	req, err := http.NewRequest(http.MethodGet, updateTagsAtomURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "SolarPanel-UpdateCheck")
	resp, err := updateClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ""
	}
	best := ""
	for _, chunk := range strings.Split(string(body), "<entry>")[1:] {
		m := atomTitleRe.FindStringSubmatch(chunk)
		if len(m) < 2 {
			continue
		}
		v := normalizeVersion(m[1])
		if v == "" {
			continue
		}
		if best == "" || compareVersion(v, best) > 0 {
			best = v
		}
	}
	return best
}

// normalizeVersion 统一成 vX.Y.Z；非数字版本（如 releases）返回空串
func normalizeVersion(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	out := make([]string, 0, 3)
	for _, p := range parts[:2] {
		if _, err := strconv.Atoi(p); err != nil {
			return ""
		}
		out = append(out, p)
	}
	patch := "0"
	if len(parts) == 3 {
		num := ""
		for _, c := range parts[2] {
			if c < '0' || c > '9' {
				break
			}
			num += string(c)
		}
		if num != "" {
			patch = num
		}
	}
	out = append(out, patch)
	return "v" + strings.Join(out, ".")
}

// compareVersion 比较两个版本号，a>b 返回 1，a<b 返回 -1
func compareVersion(a, b string) int {
	pa := versionParts(a)
	pb := versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] > pb[i] {
			return 1
		}
		if pa[i] < pb[i] {
			return -1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	var out [3]int
	s := strings.TrimPrefix(normalizeVersion(v), "v")
	if s == "" {
		return out
	}
	for i, p := range strings.SplitN(s, ".", 3) {
		if i > 2 {
			break
		}
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
