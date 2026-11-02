package handler

// 图标预热：服务启动后后台把卡片站点的图标抓一遍并落到本地缓存，
// 首页直接命中本地文件，避免「首次访问现抓现卖」造成的等待。

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

// optimizeCachedFavicons 启动时把历史缓存里的位图图标统一转成 ≤64px WebP（一次性迁移，
// 之后新抓的图标在 saveFavicon 阶段已是 WebP）。小于 2KB 的图标体积可忽略，跳过以免每次启动重复编解码。
func optimizeCachedFavicons(cfg *config.Config) {
	dir := filepath.Join(cfg.UploadDir, "favicon")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
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
}

// repairFaviconItemPaths 一次性修正 items 里指向已迁移/已清理图标文件的旧路径：
// 能按 md5 找到新扩展名就改成新路径，找不到就清空，让前端回退到按域名抓取。
func repairFaviconItemPaths(cfg *config.Config) {
	if db.DB == nil {
		return
	}
	rows, err := db.DB.Query(`SELECT id, icon_value FROM items WHERE icon_value LIKE '/frontend/uploads/favicon/%'`)
	if err != nil {
		return
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
}

// StartFaviconPrewarm 异步预热全部 favicon 类型卡片（并发 2，逐个错开，避免打满出网连接）
func StartFaviconPrewarm(cfg *config.Config) {
	go func() {
		time.Sleep(5 * time.Second) // 等 HTTP 服务与数据库就绪
		optimizeCachedFavicons(cfg)
		repairFaviconItemPaths(cfg)
		clearFaviconDataCache()
		if db.DB == nil {
			return
		}
		rows, err := db.DB.Query(`SELECT DISTINCT url FROM items WHERE icon_type = 'favicon' AND url <> ''`)
		if err != nil {
			log.Printf("[favicon] 预热查询失败: %v", err)
			return
		}
		hosts := make(map[string]bool)
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
			if host == "" || isPrivateHost(host) {
				continue
			}
			hosts[host] = true
		}
		rows.Close()
		if len(hosts) == 0 {
			return
		}

		sem := make(chan struct{}, 2)
		var wg sync.WaitGroup
		for host := range hosts {
			wg.Add(1)
			go func(h string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if checkFaviconCache(cfg, h) != "" {
					return
				}
				// 预热失败过的站点在冷却期内直接跳过，避免每次启动重复打无效请求
				if v, ok := faviconNegative.Load(h); ok {
					if t, _ := v.(time.Time); time.Since(t) < faviconNegativeTTL {
						return
					}
				}
				time.Sleep(300 * time.Millisecond)
				if saved := trySaveIcon(cfg, "https://"+h); saved != "" {
					log.Printf("[favicon] 预热完成 %s → %s", h, saved)
				} else {
					faviconNegative.Store(h, time.Now())
					log.Printf("[favicon] 预热失败 %s（%s 内不再重试，首访直接 404 走首字母占位）", h, faviconNegativeTTL)
				}
			}(host)
		}
		wg.Wait()
		log.Printf("[favicon] 预热结束，共 %d 个站点", len(hosts))
	}()
}
