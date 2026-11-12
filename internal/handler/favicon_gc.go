package handler

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"solarpanel/internal/db"
)

const (
	// 卡片图标缓存目录的对外 URL 前缀，落盘文件名为 md5(host)+扩展名
	faviconURLPrefix = "/frontend/uploads/favicon/"
	// 新写入的文件保留 7 天，避免「刚落盘、尚未入库」的竞态被误删
	faviconGCGrace = 7 * 24 * time.Hour
)

// CleanupOrphanFavicons 回收不再被任何卡片引用的 favicon 缓存文件，返回删除数量。
// 文件名是 md5(host)，同域名的多张卡片共享同一份文件，因此不能随卡片删除，
// 只能按 items.icon 的引用关系回收。
func CleanupOrphanFavicons(uploadDir string) int {
	dir := filepath.Join(uploadDir, "favicon")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	used, err := referencedFavicons()
	if err != nil {
		log.Printf("[favicon] 回收跳过：读取引用失败 %v", err)
		return 0
	}

	cutoff := time.Now().Add(-faviconGCGrace)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || used[entry.Name()] {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, entry.Name())) == nil {
			removed++
		}
	}
	if removed > 0 {
		log.Printf("[favicon] 已回收 %d 个未引用的图标缓存", removed)
	}
	return removed
}

// referencedFavicons 收集仍被卡片或分组引用的图标文件名。
// items 的图标列是 icon_value（icon_type='image'），分组是 item_groups.icon。
func referencedFavicons() (map[string]bool, error) {
	rows, err := db.DB.Query(`SELECT icon_value FROM items WHERE icon_value LIKE ?
		UNION SELECT icon FROM item_groups WHERE icon LIKE ?`,
		faviconURLPrefix+"%", faviconURLPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	used := make(map[string]bool)
	for rows.Next() {
		var icon string
		if rows.Scan(&icon) != nil {
			continue
		}
		used[filepath.Base(strings.TrimPrefix(icon, faviconURLPrefix))] = true
	}
	return used, rows.Err()
}
