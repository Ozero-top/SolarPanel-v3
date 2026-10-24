package db

import (
	"log"
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
