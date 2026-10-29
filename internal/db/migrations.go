package db

import (
	"log"

	"solarpanel/internal/urlutil"
)

// Migration 定义一次数据库升级
// Version 必须严格升序，每个版本只执行一次
// SQL 直接填 DDL/DML；复杂逻辑填 Fn（可同时填，SQL 先跑）
type Migration struct {
	Version int
	Name    string
	SQL     string
	Fn      func() error
}

// migrations 有序列表（未来升级直接 append，version 必须递增）
var migrations = []Migration{
	// v1: parent_id 字段 + backfill（v3.0.0 两级导航）
	{Version: 1, Name: "item_groups.parent_id",
		SQL: `ALTER TABLE item_groups ADD COLUMN parent_id INTEGER NOT NULL DEFAULT 0`,
		Fn:  backfillV1ParentIDs},

	// v2 示例：settings 表加 updated_at 追踪最后修改时间
	// SQLite ALTER TABLE 不允许非常量 DEFAULT，用 '' 占位 + 触发器填真时间
	{Version: 2, Name: "settings.updated_at + trigger",
		SQL: `ALTER TABLE settings ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`,
		Fn:  backfillV2SettingsUpdatedAt},

	// v3: 审计日志记录 IP 归属地 + 查询索引（分页/筛选性能）
	{Version: 3, Name: "audit_logs.region + indexes",
		SQL: `ALTER TABLE audit_logs ADD COLUMN region TEXT NOT NULL DEFAULT ''`,
		Fn:  backfillV3AuditRegion},

	// v4: 剪藏 — items.url_key + api_tokens / clip_requests / clip_contents
	{Version: 4, Name: "clip: items.url_key + token tables",
		SQL: `ALTER TABLE items ADD COLUMN url_key TEXT NOT NULL DEFAULT ''`,
		Fn:  backfillV4Clip},

	// v5: 剪藏正文快照的全文检索（P1）
	{Version: 5, Name: "clip: FTS5 index", Fn: migrateV5ClipFTS},

	// v6: 剪藏标签（P2）— item_tags 表，并把标签并入全文索引
	{Version: 6, Name: "clip: item_tags + FTS tags", Fn: migrateV6ItemTags},

	// v7: 剪藏增强（P2）— 摘要 / 自动标签 / 整页截图路径
	{Version: 7, Name: "clip: summary + auto_tags + screenshot", Fn: migrateV7ClipExtras},

	// v8: 令牌明文可再复制 + 已吊销令牌物理清理
	{Version: 8, Name: "clip: api_tokens.token_plain", Fn: migrateV8TokenPlain},

	// v9: 剪藏「稍后读」收件箱分组排到末尾（早期创建时 sort=0 会顶在最前）
	{Version: 9, Name: "clip: inbox group last", Fn: migrateV9ClipInboxLast},
}

// migrateV9ClipInboxLast 把剪藏自动创建的「稍后读」收件箱分组移到顶级分组最后
func migrateV9ClipInboxLast() error {
	if DB == nil {
		return nil
	}
	var id, sort int
	err := DB.QueryRow(`SELECT id, sort FROM item_groups
		WHERE title = '稍后读' AND parent_id = 0 AND description = '剪藏收件箱'
		ORDER BY id ASC LIMIT 1`).Scan(&id, &sort)
	if err != nil {
		return nil // 没有自动创建的收件箱，无需处理
	}
	var maxSort int
	if err := DB.QueryRow(`SELECT COALESCE(MAX(sort), 0) FROM item_groups WHERE parent_id = 0 AND id <> ?`, id).Scan(&maxSort); err != nil {
		return err
	}
	if sort > maxSort {
		return nil // 已经在最后
	}
	if _, err := DB.Exec(`UPDATE item_groups SET sort = ? WHERE id = ?`, maxSort+1, id); err != nil {
		return err
	}
	log.Println("[migrate] v9: 「稍后读」收件箱已置底")
	return nil
}

// migrateV8TokenPlain 为令牌表补明文列（后台可再次复制），并删除已吊销记录
func migrateV8TokenPlain() error {
	if DB == nil {
		return nil
	}
	has := false
	rows, err := DB.Query(`PRAGMA table_info(api_tokens)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil && name == "token_plain" {
			has = true
		}
	}
	rows.Close()
	if !has {
		if _, err := DB.Exec(`ALTER TABLE api_tokens ADD COLUMN token_plain TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	// 吊销即无效，历史吊销记录没有保留价值，直接物理删除
	if _, err := DB.Exec(`DELETE FROM api_tokens WHERE revoked_at <> '' OR revoked_at IS NULL`); err != nil {
		return err
	}
	log.Println("[migrate] v8: 令牌明文列已就绪，已清理历史吊销令牌")
	return nil
}

// migrateV7ClipExtras 为快照表补齐增强字段（幂等：列已存在时跳过）
func migrateV7ClipExtras() error {
	if DB == nil {
		return nil
	}
	cols := []struct{ name, def string }{
		{"summary", "TEXT NOT NULL DEFAULT ''"},
		{"auto_tags", "TEXT NOT NULL DEFAULT ''"},
		{"screenshot_path", "TEXT NOT NULL DEFAULT ''"},
	}
	existing := map[string]bool{}
	rows, err := DB.Query(`PRAGMA table_info(clip_contents)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil {
			existing[name] = true
		}
	}
	rows.Close()
	for _, c := range cols {
		if existing[c.name] {
			continue
		}
		if _, err := DB.Exec("ALTER TABLE clip_contents ADD COLUMN " + c.name + " " + c.def); err != nil {
			return err
		}
	}
	log.Println("[migrate] v7: 摘要 / 自动标签 / 截图字段已就绪")
	return nil
}

// migrateV5ClipFTS 建立正文快照的 FTS5 索引并全量回填。
// trigram 分词器对中文按 3 字窗口切分，中文检索可用；旧版 SQLite 无 trigram 时退回 unicode61。
func migrateV5ClipFTS() error {
	ddls := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS clip_fts USING fts5(title, excerpt, content_md, tokenize='trigram')`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS clip_fts USING fts5(title, excerpt, content_md, tokenize='unicode61 remove_diacritics 2')`,
	}
	var lastErr error
	created := false
	for _, ddl := range ddls {
		if _, err := DB.Exec(ddl); err == nil {
			created = true
			break
		} else {
			lastErr = err
		}
	}
	if !created {
		return lastErr
	}
	// 旧结构（无 tags 列）由 v6 统一重建，避免此处重建报“无 tags 列”
	if clipFTSHasTags() {
		RebuildClipFTS()
	}
	log.Println("[migrate] v5: clip_fts 全文索引已就绪")
	return nil
}

// RebuildClipFTS 全量重建快照索引（启动时数量不一致、恢复备份后调用，幂等）。
// rowid 直接使用 item_id，便于与 items 关联；tags 列拼接该卡片的全部标签。
func RebuildClipFTS() {
	if DB == nil {
		return
	}
	if _, err := DB.Exec(`DELETE FROM clip_fts`); err != nil {
		log.Printf("[clip] 清空 FTS 索引失败: %v", err)
		return
	}
	_, err := DB.Exec(`INSERT INTO clip_fts(rowid, title, excerpt, content_md, tags)
		SELECT c.item_id, COALESCE(i.title, ''), c.excerpt, c.content_md,
		       COALESCE((SELECT group_concat(t.tag, ' ') FROM item_tags t WHERE t.item_id = c.item_id), '')
		FROM clip_contents c LEFT JOIN items i ON i.id = c.item_id`)
	if err != nil {
		log.Printf("[clip] 重建 FTS 索引失败: %v", err)
		return
	}
	log.Println("[clip] FTS 索引已重建")
}

// EnsureClipFTS 启动时校验索引条数，与快照表不一致则重建
func EnsureClipFTS() {
	if DB == nil {
		return
	}
	// 旧库的 clip_fts 只有三列（无 tags）时，按新结构重建
	if !clipFTSHasTags() {
		if err := recreateClipFTS(); err != nil {
			log.Printf("[clip] 重建 FTS 表结构失败: %v", err)
			return
		}
		RebuildClipFTS()
		return
	}
	var indexed, total int
	DB.QueryRow(`SELECT COUNT(*) FROM clip_fts`).Scan(&indexed)
	DB.QueryRow(`SELECT COUNT(*) FROM clip_contents`).Scan(&total)
	if indexed != total {
		log.Printf("[clip] FTS 索引条数不一致（%d/%d），重建", indexed, total)
		RebuildClipFTS()
	}
}

// migrateV6ItemTags 建立标签表，并把 tags 列并入全文索引（FTS5 列变更需删表重建）
func migrateV6ItemTags() error {
	if _, err := DB.Exec(`CREATE TABLE IF NOT EXISTS item_tags (
		item_id INTEGER NOT NULL,
		tag     TEXT NOT NULL,
		PRIMARY KEY (item_id, tag),
		FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
	)`); err != nil {
		return err
	}
	if _, err := DB.Exec(`CREATE INDEX IF NOT EXISTS idx_item_tags_tag ON item_tags(tag)`); err != nil {
		return err
	}
	if err := recreateClipFTS(); err != nil {
		return err
	}
	RebuildClipFTS()
	log.Println("[migrate] v6: item_tags 与全文索引(tags) 已就绪")
	return nil
}

// recreateClipFTS 按四列结构（含 tags）重建 FTS 虚表，trigram 不可用时退回 unicode61
func recreateClipFTS() error {
	if DB == nil {
		return nil
	}
	DB.Exec(`DROP TABLE IF EXISTS clip_fts`)
	ddls := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS clip_fts USING fts5(title, excerpt, content_md, tags, tokenize='trigram')`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS clip_fts USING fts5(title, excerpt, content_md, tags, tokenize='unicode61 remove_diacritics 2')`,
	}
	var lastErr error
	for _, ddl := range ddls {
		if _, err := DB.Exec(ddl); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

// clipFTSHasTags 检查当前 FTS 虚表是否包含 tags 列
func clipFTSHasTags() bool {
	rows, err := DB.Query(`PRAGMA table_info(clip_fts)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == "tags" {
			return true
		}
	}
	return false
}

// backfillV4Clip v4 迁移专用：确保 url_key 列与索引存在，并回填已有卡片
func backfillV4Clip() error {
	has := false
	rows, err := DB.Query("PRAGMA table_info(items)")
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var cname, ctype string
		var dflt any
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if cname == "url_key" {
			has = true
			break
		}
	}
	rows.Close()
	if !has {
		if _, err := DB.Exec("ALTER TABLE items ADD COLUMN url_key TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_items_url_key ON items(url_key)`)
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)`)
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_clip_contents_url_key ON clip_contents(url_key)`)
	log.Println("[migrate] v4: items.url_key + 剪藏表已就绪")

	BackfillURLKeys()
	return nil
}

// BackfillURLKeys 为 url_key 为空的卡片补算查重键（幂等，启动时也会调用）。
// 先收集再关闭游标，避免在 SQLite 读事务中写入造成锁等待。
func BackfillURLKeys() {
	if DB == nil {
		return
	}
	type row struct {
		id     int
		url    string
		lanURL string
	}
	var rowsData []row
	rows, err := DB.Query("SELECT id, url, lan_url FROM items WHERE url_key = ''")
	if err != nil {
		return
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.url, &r.lanURL); err == nil {
			rowsData = append(rowsData, r)
		}
	}
	rows.Close()
	if len(rowsData) == 0 {
		return
	}

	tx, err := DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	filled := 0
	for _, r := range rowsData {
		src := r.url
		if src == "" {
			src = r.lanURL
		}
		if src == "" {
			continue
		}
		key := urlutil.Key(src)
		if key == "" {
			continue
		}
		if _, err := tx.Exec("UPDATE items SET url_key = ? WHERE id = ?", key, r.id); err != nil {
			continue
		}
		filled++
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[migrate] url_key 回填失败: %v", err)
		return
	}
	if filled > 0 {
		log.Printf("[migrate] url_key 回填 %d 张卡片", filled)
	}
}

// backfillV2SettingsUpdatedAt v2 迁移专用：
// 1) 确保 updated_at 列存在（ALTER TABLE 可能因 duplicate column 被 skip）
// 2) 所有现有行 updated_at 保持 DEFAULT（当前时间），无需额外回填
// 3) 创建 AFTER UPDATE 触发器，用 WHEN 守卫避免自递归
func backfillV2SettingsUpdatedAt() error {
	// 确认列存在
	var has bool
	rows, err := DB.Query("PRAGMA table_info(settings)")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var cname, ctype string
		var notnull int
		var dflt string
		var pk int
		rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk)
		if cname == "updated_at" {
			has = true
			break
		}
	}
	if !has {
		if _, err := DB.Exec(`ALTER TABLE settings ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}

	// 回填所有现有行：updated_at = 当前时间（SQLite ALTER TABLE 不接受非常量 DEFAULT，由 Fn 填真值）
	DB.Exec(`UPDATE settings SET updated_at = datetime('now','localtime') WHERE updated_at = ''`)

	// 创建 AFTER UPDATE 触发器：WHEN 守卫避免自递归（触发器触发 UPDATE updated_at → 又触发自己）
	DB.Exec(`DROP TRIGGER IF EXISTS trg_settings_updated_at`)
	DB.Exec(`CREATE TRIGGER trg_settings_updated_at
		AFTER UPDATE OF config_value ON settings
		WHEN NEW.config_value != OLD.config_value
		BEGIN
			UPDATE settings SET updated_at = datetime('now','localtime') WHERE id = NEW.id;
		END`)

	log.Println("[migrate] v2: settings.updated_at + trigger 已就绪")
	return nil
}

// backfillV3AuditRegion v3 迁移专用：确保 region 列存在，并建立分页/筛选索引
func backfillV3AuditRegion() error {
	has := false
	rows, err := DB.Query("PRAGMA table_info(audit_logs)")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var cname, ctype string
		var dflt any
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if cname == "region" {
			has = true
			break
		}
	}
	if !has {
		if _, err := DB.Exec("ALTER TABLE audit_logs ADD COLUMN region TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC)`)
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs(action)`)
	log.Println("[migrate] v3: audit_logs.region + indexes 已就绪")
	return nil
}

// initSchemaVersionsTable 确保版本追踪表存在
func initSchemaVersionsTable() error {
	_, err := DB.Exec(`CREATE TABLE IF NOT EXISTS schema_versions (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
	)`)
	return err
}

// appliedVersions 返回已执行的版本号集合
func appliedVersions() (map[int]bool, error) {
	rows, err := DB.Query("SELECT version FROM schema_versions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		m[v] = true
	}
	return m, nil
}

// RunMigrations 跑所有未执行过的迁移（幂等）
func RunMigrations() {
	if err := initSchemaVersionsTable(); err != nil {
		log.Printf("[migrate] schema_versions 创建失败: %v", err)
		return
	}

	applied, err := appliedVersions()
	if err != nil {
		log.Printf("[migrate] 读取版本失败: %v", err)
		return
	}

	ran := 0
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		log.Printf("[migrate] v%d %s ...", m.Version, m.Name)

		if m.SQL != "" {
			if _, err := DB.Exec(m.SQL); err != nil {
				// 字段可能已被建表时建过（GORM 风格），忽略 "duplicate column" 错误
				log.Printf("[migrate] v%d SQL: %v", m.Version, err)
			}
		}

		if m.Fn != nil {
			if err := m.Fn(); err != nil {
				log.Printf("[migrate] v%d fn: %v", m.Version, err)
				continue
			}
		}

		if _, err := DB.Exec("INSERT INTO schema_versions (version, name) VALUES (?, ?)", m.Version, m.Name); err != nil {
			log.Printf("[migrate] v%d 记录失败: %v", m.Version, err)
			continue
		}
		log.Printf("[migrate] v%d ✅ %s 已应用", m.Version, m.Name)
		ran++
	}

	log.Printf("[migrate] 已是最新 (%d 套迁移已全部应用)", len(migrations))
}

// backfillV1ParentIDs v1 迁移专用：先加列（通过 SQL）再 backfill
func backfillV1ParentIDs() error {
	// 确认列存在（ALTER TABLE 可能因 duplicate column 被 skip）
	var has bool
	rows, err := DB.Query("PRAGMA table_info(item_groups)")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var cname, ctype string
		var notnull int
		var dflt string
		var pk int
		rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk)
		if cname == "parent_id" {
			has = true
			break
		}
	}
	if !has {
		if _, err := DB.Exec("ALTER TABLE item_groups ADD COLUMN parent_id INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	return nil
}
