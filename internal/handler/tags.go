package handler

import (
	"strings"
	"unicode/utf8"

	"solarpanel/internal/db"
)

// 标签约束：单卡最多 20 个，单个标签最多 32 字
const (
	maxTagsPerItem = 20
	maxTagRunes    = 32
)

// normalizeTags 清洗标签：去 # 前缀、去逗号、去重（忽略大小写）、限制长度与数量
func normalizeTags(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		tag := strings.TrimSpace(raw)
		tag = strings.TrimPrefix(tag, "#")
		tag = strings.NewReplacer(",", "", "，", "", ";", "", "；", "").Replace(tag)
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > maxTagRunes {
			tag = string([]rune(tag)[:maxTagRunes])
		}
		k := strings.ToLower(tag)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, tag)
		if len(out) >= maxTagsPerItem {
			break
		}
	}
	return out
}

// splitTagList 拆分 group_concat 得到的标签串
func splitTagList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// loadItemTags 读取单张卡片的标签（字典序，保证输出稳定）
func loadItemTags(itemID int) []string {
	out := []string{}
	if db.DB == nil || itemID <= 0 {
		return out
	}
	rows, err := db.DB.Query(`SELECT tag FROM item_tags WHERE item_id = ? ORDER BY tag`, itemID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		if rows.Scan(&tag) == nil && tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// loadTagsForItems 批量读取标签，避免列表页 N+1 查询
func loadTagsForItems(itemIDs []int) map[int][]string {
	out := make(map[int][]string, len(itemIDs))
	if db.DB == nil || len(itemIDs) == 0 {
		return out
	}
	placeholders := make([]string, 0, len(itemIDs))
	args := make([]any, 0, len(itemIDs))
	for _, id := range itemIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := db.DB.Query(`SELECT item_id, tag FROM item_tags WHERE item_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY tag`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var tag string
		if rows.Scan(&id, &tag) == nil {
			out[id] = append(out[id], tag)
		}
	}
	return out
}

// setItemTags 覆盖写入标签，并刷新该卡片的全文索引（标签参与检索）
func setItemTags(itemID int, tags []string) error {
	if db.DB == nil || itemID <= 0 {
		return nil
	}
	tags = normalizeTags(tags)
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM item_tags WHERE item_id = ?`, itemID); err != nil {
		tx.Rollback()
		return err
	}
	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO item_tags (item_id, tag) VALUES (?,?)`, itemID, tag); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	refreshClipFTS(itemID)
	return nil
}

// deleteItemTags 删除卡片标签（外键未生效的历史库也能清理干净）
func deleteItemTags(itemID int) {
	if db.DB == nil || itemID <= 0 {
		return
	}
	db.DB.Exec(`DELETE FROM item_tags WHERE item_id = ?`, itemID)
}

// refreshClipFTS 卡片标题/标签变化后，按最新数据重写该行的全文索引；无快照则跳过
func refreshClipFTS(itemID int) {
	if db.DB == nil || itemID <= 0 || !clipFTSAvailable() {
		return
	}
	var title, excerpt, contentMD string
	err := db.DB.QueryRow(`SELECT COALESCE(i.title,''), c.excerpt, c.content_md
		FROM clip_contents c LEFT JOIN items i ON i.id = c.item_id
		WHERE c.item_id = ?`, itemID).Scan(&title, &excerpt, &contentMD)
	if err != nil {
		return
	}
	indexClipFTS(itemID, title, excerpt, contentMD)
}
