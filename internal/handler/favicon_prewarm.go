package handler

// 图标预热：服务启动后后台把卡片站点的图标抓一遍并落到本地缓存，
// 首页直接命中本地文件，避免「首次访问现抓现卖」造成的等待。

import (
	"log"
	"net/url"
	"sync"
	"time"

	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

// StartFaviconPrewarm 异步预热全部 favicon 类型卡片（并发 2，逐个错开，避免打满出网连接）
func StartFaviconPrewarm(cfg *config.Config) {
	go func() {
		time.Sleep(5 * time.Second) // 等 HTTP 服务与数据库就绪
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
				time.Sleep(300 * time.Millisecond)
				if saved := trySaveIcon(cfg, "https://"+h); saved != "" {
					log.Printf("[favicon] 预热完成 %s → %s", h, saved)
				}
			}(host)
		}
		wg.Wait()
		log.Printf("[favicon] 预热结束，共 %d 个站点", len(hosts))
	}()
}
