package handler

// 图标预热：服务启动后后台把卡片站点的图标抓一遍并落到本地缓存，
// 首页直接命中本地文件，避免「首次访问现抓现卖」造成的等待。

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

// optimizeCachedFavicons 启动时把历史缓存里的位图图标统一转成 ≤64px WebP（一次性迁移，
// 之后新抓的图标在 saveFavicon 阶段已是 WebP）。小于 2KB 的图标体积可忽略，跳过以免每次启动重复编解码。
func optimizeCachedFavicons(cfg *config.Config) (converted, cleaned int) {
	dir := filepath.Join(cfg.UploadDir, "favicon")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	n := 0
	bad := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".png", ".jpg", ".gif", ".ico":
		default:
			continue // SVG / 已是 WEBP，保持原样
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, e.Name())
		oldURL := "/frontend/uploads/favicon/" + e.Name()
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// 图标站偶尔返回 HTML 错误页被当成 .ico 存下来，这类文件直接清掉
		if !validIconData(data, ext) {
			os.Remove(p)
			bad++
			if db.DB != nil {
				db.DB.Exec("UPDATE items SET icon_value='' WHERE icon_value=?", oldURL)
			}
			continue
		}
		if info.Size() < 2*1024 {
			continue
		}
		out, newExt := optimizeFaviconImage(data, ext)
		if newExt == ext && len(out) >= len(data) {
			continue
		}
		np := filepath.Join(dir, strings.TrimSuffix(e.Name(), ext)+newExt)
		if err := os.WriteFile(np, out, 0644); err != nil {
			continue
		}
		if np != p {
			if db.DB != nil {
				db.DB.Exec("UPDATE items SET icon_value=? WHERE icon_value=?",
					"/frontend/uploads/favicon/"+filepath.Base(np), oldURL)
			}
			os.Remove(p)
		}
		n++
	}
	if n > 0 {
		log.Printf("[favicon] 历史缓存图标已压缩 %d 个", n)
	}
	if bad > 0 {
		log.Printf("[favicon] 已清理 %d 个无效图标文件（HTML/损坏内容）", bad)
	}
	return n, bad
}

// repairFaviconItemPaths 一次性修正 items 里指向已迁移/已清理图标文件的旧路径：
// 能按 md5 找到新扩展名就改成新路径，找不到就清空，让前端回退到按域名抓取。
func repairFaviconItemPaths(cfg *config.Config) int {
	if db.DB == nil {
		return 0
	}
	rows, err := db.DB.Query(`SELECT id, icon_value FROM items WHERE icon_value LIKE '/frontend/uploads/favicon/%'`)
	if err != nil {
		return 0
	}
	type fix struct {
		id    int64
		value string
	}
	var fixes []fix
	dir := filepath.Join(cfg.UploadDir, "favicon")
	for rows.Next() {
		var id int64
		var v string
		if rows.Scan(&id, &v) != nil {
			continue
		}
		rel := strings.TrimPrefix(v, "/frontend/uploads/favicon/")
		if strings.Contains(rel, "/") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			continue
		}
		stem := strings.TrimSuffix(rel, filepath.Ext(rel))
		found := ""
		for _, e := range faviconExts {
			if _, err := os.Stat(filepath.Join(dir, stem+e)); err == nil {
				found = "/frontend/uploads/favicon/" + stem + e
				break
			}
		}
		fixes = append(fixes, fix{id: id, value: found})
	}
	rows.Close()
	for _, f := range fixes {
		db.DB.Exec("UPDATE items SET icon_value=? WHERE id=?", f.value, f.id)
	}
	if len(fixes) > 0 {
		log.Printf("[favicon] 修正 %d 条失效图标路径", len(fixes))
	}
	return len(fixes)
}

// StartFaviconPrewarm 异步预热全部 favicon 类型卡片。
// 启动后延迟 45s 再跑：首访阶段（页面加载 + 按需抓图标 + WebP 编码）不与预热抢 NAS 的 CPU 和出网带宽；
// 预热本身并发 1、逐个间隔 500ms，避免同一时刻对几十个站点发起多候选抓取。
func StartFaviconPrewarm(cfg *config.Config) {
	go func() {
		time.Sleep(45 * time.Second) // 等首访高峰过去，再开始后台抓取与历史图标重编码
		optimizeCachedFavicons(cfg)
		repairFaviconItemPaths(cfg)
		clearFaviconDataCache()
		hosts := faviconHostsFromItems()
		if len(hosts) == 0 {
			return
		}
		warmFaviconHosts(cfg, hosts, false, 1, 500*time.Millisecond)
		log.Printf("[favicon] 预热结束，共 %d 个站点", len(hosts))
	}()
}

// faviconHostsFromItems 取所有 favicon 卡片域名（去重、跳过内网）
func faviconHostsFromItems() []string {
	if db.DB == nil {
		return nil
	}
	rows, err := db.DB.Query(`SELECT DISTINCT url FROM items WHERE icon_type = 'favicon' AND url <> ''`)
	if err != nil {
		log.Printf("[favicon] 预热查询失败: %v", err)
		return nil
	}
	dealt := make(map[string]bool)
	var hosts []string
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		host := u.Hostname()
		if host == "" || dealt[host] || isPrivateHost(host) {
			continue
		}
		dealt[host] = true
		hosts = append(hosts, host)
	}
	rows.Close()
	return hosts
}

// warmFaviconHosts 抓取缺失图标（conc 并发、每个 host 前 gap 错开）。ignoreNegative=true 时忽略失败冷却。
func warmFaviconHosts(cfg *config.Config, hosts []string, ignoreNegative bool, conc int, gap time.Duration) (saved, failed int) {
	if conc < 1 {
		conc = 1
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, host := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if checkFaviconCache(cfg, h) != "" {
				return
			}
			if !ignoreNegative {
				if v, ok := faviconNegative.Load(h); ok {
					if t, _ := v.(time.Time); time.Since(t) < faviconNegativeTTL {
						return
					}
				}
			}
			if gap > 0 {
				time.Sleep(gap)
			}
			if s := trySaveIcon(cfg, "https://"+h); s != "" {
				log.Printf("[favicon] 预热完成 %s → %s", h, s)
				mu.Lock()
				saved++
				mu.Unlock()
			} else {
				faviconNegative.Store(h, time.Now())
				log.Printf("[favicon] 预热失败 %s（%s 内不再重试，首访直接 404 走首字母占位）", h, faviconNegativeTTL)
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}(host)
	}
	wg.Wait()
	return saved, failed
}

// FaviconSweepStatus 图标体检结果（供后台轮询）
type FaviconSweepStatus struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Converted  int    `json:"converted"`
	Cleaned    int    `json:"cleaned"`
	Repaired   int    `json:"repaired"`
	Refetched  int    `json:"refetched"`
	Failed     int    `json:"failed"`
	Total      int    `json:"total"`
}

var (
	sweepMu     sync.Mutex
	sweepStatus = FaviconSweepStatus{}
)

// StartFaviconSweep 后台执行一次图标体检：转换 + 清理 + 修复路径 + 清冷却补抓。
func StartFaviconSweep(cfg *config.Config) (FaviconSweepStatus, bool) {
	sweepMu.Lock()
	if sweepStatus.Running {
		st := sweepStatus
		sweepMu.Unlock()
		return st, false
	}
	sweepStatus = FaviconSweepStatus{Running: true, StartedAt: time.Now().Format("15:04:05")}
	sweepMu.Unlock()

	go func() {
		res := FaviconSweepStatus{Running: true, StartedAt: time.Now().Format("15:04:05")}
		res.Converted, res.Cleaned = optimizeCachedFavicons(cfg)
		res.Repaired = repairFaviconItemPaths(cfg)
		clearFaviconDataCache()
		hosts := faviconHostsFromItems()
		res.Total = len(hosts)
		for _, h := range hosts {
			faviconNegative.Delete(h)
		}
		res.Refetched, res.Failed = warmFaviconHosts(cfg, hosts, true, 2, 300*time.Millisecond)
		res.Running = false
		res.FinishedAt = time.Now().Format("15:04:05")
		sweepMu.Lock()
		sweepStatus = res
		sweepMu.Unlock()
		log.Printf("[favicon] 图标体检完成：转换 %d，清理 %d，修复 %d，补抓 %d，失败 %d",
			res.Converted, res.Cleaned, res.Repaired, res.Refetched, res.Failed)
	}()
	return sweepStatus, true
}

func faviconSweepSnapshot() FaviconSweepStatus {
	sweepMu.Lock()
	defer sweepMu.Unlock()
	return sweepStatus
}

// FaviconSweepHandler 后台「图标体检」入口：GET 取状态，POST 启动
func FaviconSweepHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sess := auth.GetSessionFromRequest(r); sess == nil || sess.Role != "admin" {
			Fail(w, "仅管理员可执行图标体检")
			return
		}
		switch r.Method {
		case http.MethodGet:
			OK(w, faviconSweepSnapshot())
		case http.MethodPost:
			st, started := StartFaviconSweep(cfg)
			if !started {
				Fail(w, "图标体检正在进行中，请稍后")
				return
			}
			OK(w, st)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
