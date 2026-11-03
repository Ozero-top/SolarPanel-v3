package handler

import (
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"

	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

const (
	legacyIconMigrateWorkers = 3
	// legacyIconMigrateBatch 单次启动最多处理的卡片数，避免大批量导入后一次性打满出站请求
	legacyIconMigrateBatch = 30
	// legacyIconSkipKey 记录迁移失败的卡片 ID（逗号分隔），避免每次重启重复抓取慢站
	legacyIconSkipKey = "icon_migrate_skipped"
	legacyIconSkipMax = 500
)

// MigrateLegacyFaviconItems 后台把旧的 favicon 类型卡片迁移为本地图片：
// 扫描 icon_type='favicon' 的卡片，取第一个候选下载落盘，成功后改为 image。
// 失败记录到 icon_migrate_skipped 并在后续启动中跳过，不阻塞启动。
func MigrateLegacyFaviconItems(cfg *config.Config) {
	go func() {
		skip := loadIconMigrateSkip()
		rows, err := db.DB.Query(`SELECT id, url, lan_url FROM items WHERE icon_type = 'favicon' ORDER BY id ASC`)
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
			if skip[id] {
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
			if len(list) >= legacyIconMigrateBatch {
				break
			}
		}
		rows.Close()
		if len(list) == 0 {
			return
		}
		log.Printf("[icon-migrate] 本次待迁移 %d 张 favicon 卡片（已跳过失败 %d 张）", len(list), len(skip))

		sem := make(chan struct{}, legacyIconMigrateWorkers)
		failedCh := make(chan int, len(list))
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
					failedCh <- it.id
					return
				}
				saved, err := downloadFavicon(cfg, cands[0])
				if err != nil {
					log.Printf("[icon-migrate] 卡片 #%d 下载图标失败: %v", it.id, err)
					failedCh <- it.id
					return
				}
				if _, err := db.DB.Exec(`UPDATE items SET icon_type='image', icon_value=? WHERE id=?`, saved, it.id); err != nil {
					log.Printf("[icon-migrate] 卡片 #%d 更新失败: %v", it.id, err)
					failedCh <- it.id
					return
				}
				log.Printf("[icon-migrate] 卡片 #%d 已迁移: %s", it.id, saved)
			}(it)
		}
		wg.Wait()
		close(failedCh)

		failed := 0
		for id := range failedCh {
			skip[id] = true
			failed++
		}
		if failed > 0 {
			saveIconMigrateSkip(skip)
		}
		log.Printf("[icon-migrate] favicon 卡片迁移完成（本次失败 %d 张已记录，不再重试）", failed)
	}()
}

// loadIconMigrateSkip 读取历史迁移失败的卡片 ID
func loadIconMigrateSkip() map[int]bool {
	set := make(map[int]bool)
	var raw string
	if err := db.DB.QueryRow("SELECT config_value FROM settings WHERE config_name=?", legacyIconSkipKey).Scan(&raw); err != nil {
		return set
	}
	for _, s := range strings.Split(raw, ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && id > 0 {
			set[id] = true
		}
	}
	return set
}

// saveIconMigrateSkip 落库失败 ID，超过上限时保留较小的 ID（保证集合有界）
func saveIconMigrateSkip(set map[int]bool) {
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	if len(ids) > legacyIconSkipMax {
		ids = ids[:legacyIconSkipMax]
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.Itoa(id))
	}
	val := strings.Join(parts, ",")

	res, err := db.DB.Exec("UPDATE settings SET config_value=? WHERE config_name=?", val, legacyIconSkipKey)
	if err != nil {
		log.Printf("[icon-migrate] 保存跳过列表失败: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		db.DB.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", legacyIconSkipKey, val)
	}
}
