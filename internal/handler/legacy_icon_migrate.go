package handler

import (
	"log"
	"strings"
	"sync"

	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

const legacyIconMigrateWorkers = 3

// MigrateLegacyFaviconItems 后台把旧的 favicon 类型卡片迁移为本地图片：
// 扫描 icon_type='favicon' 的卡片，取第一个候选下载落盘，成功后改为 image。
// 失败只记录日志并跳过，不阻塞启动、不重试。
func MigrateLegacyFaviconItems(cfg *config.Config) {
	go func() {
		rows, err := db.DB.Query(`SELECT id, url, lan_url FROM items WHERE icon_type = 'favicon'`)
		if err != nil {
			log.Printf("[icon-migrate] 查询待迁移卡片失败: %v", err)
			return
		}
		type legacyItem struct {
			id      int
			pageURL string
		}
		var list []legacyItem
		for rows.Next() {
			var id int
			var pageURL, lanURL string
			if err := rows.Scan(&id, &pageURL, &lanURL); err != nil {
				continue
			}
			page := strings.TrimSpace(pageURL)
			if page == "" {
				page = strings.TrimSpace(lanURL)
			}
			if page == "" {
				continue
			}
			list = append(list, legacyItem{id: id, pageURL: page})
		}
		rows.Close()
		if len(list) == 0 {
			return
		}
		log.Printf("[icon-migrate] 发现 %d 张 favicon 卡片待迁移", len(list))

		sem := make(chan struct{}, legacyIconMigrateWorkers)
		var wg sync.WaitGroup
		for _, it := range list {
			wg.Add(1)
			sem <- struct{}{}
			go func(it legacyItem) {
				defer wg.Done()
				defer func() { <-sem }()

				cands, err := siteFaviconCandidates(it.pageURL)
				if err != nil || len(cands) == 0 {
					log.Printf("[icon-migrate] 卡片 #%d 获取图标候选失败: %v", it.id, err)
					return
				}
				saved, err := downloadFavicon(cfg, cands[0])
				if err != nil {
					log.Printf("[icon-migrate] 卡片 #%d 下载图标失败: %v", it.id, err)
					return
				}
				if _, err := db.DB.Exec(`UPDATE items SET icon_type='image', icon_value=? WHERE id=?`, saved, it.id); err != nil {
					log.Printf("[icon-migrate] 卡片 #%d 更新失败: %v", it.id, err)
					return
				}
				log.Printf("[icon-migrate] 卡片 #%d 已迁移: %s", it.id, saved)
			}(it)
		}
		wg.Wait()
		log.Printf("[icon-migrate] favicon 卡片迁移完成")
	}()
}
