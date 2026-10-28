package db

import (
	"database/sql"
	"log"
	"path/filepath"
	"strings"
	"time"

	"solarpanel/internal/config"
	"solarpanel/internal/settings"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// buildDSN 把 PRAGMA 写进 DSN。
// 关键：database/sql 是连接池，DB.Exec("PRAGMA ...") 只作用于当时借出的那一条连接，
// 其余连接仍是默认值（busy_timeout=0）。并发时读到正在写的库会立刻返回 SQLITE_BUSY，
// 表现为接口随机 500。写进 DSN 后每条新连接都会应用。
func buildDSN(path string) string {
	pragmas := []string{
		"_pragma=busy_timeout(15000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(ON)",
		"_pragma=cache_size(-20000)",
		"_pragma=temp_store(MEMORY)",
	}
	return "file:" + filepath.ToSlash(path) + "?" + strings.Join(pragmas, "&")
}

var DB *sql.DB

// expectedColumns 各表必须具备的列（列名 → 列定义）。
// 兼容旧版（V2.x / 早期版本）数据库：CREATE TABLE IF NOT EXISTS 对已存在的表不生效，
// 旧表缺失的列不会被补上，查询就会报 "no such column: xxx"（例如 items.is_hidden）。
// 启动时按 PRAGMA table_info 检测并 ALTER TABLE 补齐，既保留旧数据又能正常读写。
var expectedColumns = map[string][]struct{ Name, Def string }{
	"items": {
		{"lan_url", "TEXT NOT NULL DEFAULT ''"},
		{"description", "TEXT NOT NULL DEFAULT ''"},
		{"icon_type", "TEXT NOT NULL DEFAULT 'image'"},
		{"icon_value", "TEXT NOT NULL DEFAULT ''"},
		{"icon_bg", "TEXT NOT NULL DEFAULT ''"},
		{"open_method", "INTEGER NOT NULL DEFAULT 2"},
		{"sort", "INTEGER NOT NULL DEFAULT 0"},
		{"is_hidden", "INTEGER NOT NULL DEFAULT 0"},
		{"user_id", "INTEGER NOT NULL DEFAULT 1"},
		{"created_at", "TEXT NOT NULL DEFAULT ''"},
		{"url_key", "TEXT NOT NULL DEFAULT ''"},
	},
	"item_groups": {
		{"parent_id", "INTEGER NOT NULL DEFAULT 0"},
		{"icon", "TEXT NOT NULL DEFAULT ''"},
		{"description", "TEXT NOT NULL DEFAULT ''"},
		{"sort", "INTEGER NOT NULL DEFAULT 0"},
		{"is_visible", "INTEGER NOT NULL DEFAULT 1"},
		{"user_id", "INTEGER NOT NULL DEFAULT 1"},
		{"created_at", "TEXT NOT NULL DEFAULT ''"},
	},
	"users": {
		{"name", "TEXT NOT NULL DEFAULT ''"},
		{"status", "INTEGER NOT NULL DEFAULT 1"},
		{"role", "TEXT NOT NULL DEFAULT 'admin'"},
		{"totp_secret", "TEXT NOT NULL DEFAULT ''"},
		{"created_at", "TEXT NOT NULL DEFAULT ''"},
	},
	"settings": {
		{"config_value", "TEXT"},
		{"updated_at", "TEXT NOT NULL DEFAULT ''"},
	},
	"audit_logs": {
		{"target", "TEXT NOT NULL DEFAULT ''"},
		{"result", "TEXT NOT NULL DEFAULT 'success'"},
		{"actor", "TEXT NOT NULL DEFAULT ''"},
		{"actor_role", "TEXT NOT NULL DEFAULT ''"},
		{"ip", "TEXT NOT NULL DEFAULT ''"},
		{"region", "TEXT NOT NULL DEFAULT ''"},
		{"user_agent", "TEXT NOT NULL DEFAULT ''"},
		{"detail", "TEXT NOT NULL DEFAULT ''"},
		{"created_at", "TEXT NOT NULL DEFAULT ''"},
	},
	"custom_feeds": {
		{"sort", "INTEGER NOT NULL DEFAULT 0"},
		{"enabled", "INTEGER NOT NULL DEFAULT 1"},
		{"created_at", "TEXT NOT NULL DEFAULT ''"},
	},
}

// ensureColumns 为旧库补齐缺失列（幂等，表不存在时跳过）
func ensureColumns() {
	for table, cols := range expectedColumns {
		existing := map[string]bool{}
		rows, err := DB.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			log.Printf("[db] 读取表结构失败 %s: %v", table, err)
			continue
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt any
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
				existing[name] = true
			}
		}
		rows.Close()
		if len(existing) == 0 {
			continue // 表尚未创建，由 DDL 负责
		}
		for _, c := range cols {
			if existing[c.Name] {
				continue
			}
			if _, err := DB.Exec("ALTER TABLE " + table + " ADD COLUMN " + c.Name + " " + c.Def); err != nil {
				log.Printf("[db] 补列失败 %s.%s: %v", table, c.Name, err)
			} else {
				log.Printf("[db] 旧库补列 %s.%s ✅", table, c.Name)
			}
		}
	}
}

// Init 初始化 SQLite + 建表 + 迁移 + 种子
func Init(cfg *config.Config) error {
	var err error
	DB, err = sql.Open("sqlite", buildDSN(cfg.DBPath))
	if err != nil {
		return err
	}
	// SQLite WAL 模式下推荐 10-25；过小会导致并发请求竞争连接而卡住
	DB.SetMaxOpenConns(15)
	// 连接闲置后关闭释放文件句柄（SQLite 文件锁场景下有用）
	DB.SetConnMaxIdleTime(5 * time.Minute)

	// PRAGMA 已随 DSN 下发到每条连接；这里只做一次健康检查并记录实际生效值。
	// 若 NAS 的数据目录挂在网络盘（SMB/NFS）上，WAL 可能无法启用而退回 delete 模式，
	// 此时并发读写会更容易撞锁，日志里能直接看出来。
	var journalMode string
	if e := DB.QueryRow("PRAGMA journal_mode").Scan(&journalMode); e != nil {
		log.Printf("[db] 读取 journal_mode 失败: %v", e)
	} else {
		log.Printf("[db] journal_mode = %s", journalMode)
	}

	log.Println("[db] SQLite connected:", cfg.DBPath)

	// 执行建表
	for i, ddl := range schemaDDL {
		if _, err = DB.Exec(ddl); err != nil {
			log.Printf("[db] DDL[%d] err: %v\n  %s\n", i, err, ddl)
		} else {
			log.Printf("[db] DDL[%d] OK", i)
		}
	}

	log.Println("[db] 校验并补齐旧库缺失的列...")
	ensureColumns()

	log.Println("[db] 执行迁移...")
	// 运行有序迁移
	RunMigrations()

	log.Println("[db] 执行种子...")
	// 种子数据
	seedIfEmpty()

	log.Println("[db] backfillParentIDs...")
	// 回填 parent_id 孤儿分组（幂等）
	backfillParentIDs()

	log.Println("[db] ✅ 数据库就绪")
	return nil
}

// NowStr 当前时间（本地时区，SQLite 兼容）
func NowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// Close 关闭数据库连接
func Close() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}

// schemaDDL 所有建表语句（IF NOT EXISTS）
var schemaDDL = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		status INTEGER NOT NULL DEFAULT 1,
		role TEXT NOT NULL DEFAULT 'admin',
		totp_secret TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		config_name TEXT NOT NULL UNIQUE,
		config_value TEXT,
		updated_at TEXT NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS item_groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		parent_id INTEGER NOT NULL DEFAULT 0,
		title TEXT NOT NULL,
		icon TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		sort INTEGER NOT NULL DEFAULT 0,
		is_visible INTEGER NOT NULL DEFAULT 1,
		user_id INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		url TEXT NOT NULL DEFAULT '',
		lan_url TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		icon_type TEXT NOT NULL DEFAULT 'image',
		icon_value TEXT NOT NULL DEFAULT '',
		icon_bg TEXT NOT NULL DEFAULT '',
		open_method INTEGER NOT NULL DEFAULT 2,
		sort INTEGER NOT NULL DEFAULT 0,
		is_hidden INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT '',
		url_key TEXT NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS custom_feeds (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		sort INTEGER NOT NULL DEFAULT 0,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		action TEXT NOT NULL,
		target TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT 'success',
		actor TEXT NOT NULL DEFAULT '',
		actor_role TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '',
		region TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		detail TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS schema_versions (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS rate_limits (
		ip TEXT NOT NULL,
		endpoint TEXT NOT NULL,
		count INTEGER NOT NULL DEFAULT 0,
		window_start TEXT NOT NULL,
		PRIMARY KEY (ip, endpoint)
	)`,

	`CREATE TABLE IF NOT EXISTS trusted_devices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		token_hash TEXT NOT NULL,
		device_name TEXT NOT NULL DEFAULT '',
		ip_snippet TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '',
		last_used_at TEXT,
		expires_at TEXT NOT NULL
	)`,

	// 剪藏：个人访问令牌（只存 SHA-256，不存明文）
	`CREATE TABLE IF NOT EXISTS api_tokens (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id       INTEGER NOT NULL,
		name          TEXT NOT NULL,
		token_hash    TEXT NOT NULL UNIQUE,
		token_prefix  TEXT NOT NULL,
		scopes        TEXT NOT NULL DEFAULT 'clip:write',
		expires_at    TEXT NOT NULL DEFAULT '',
		last_used_at  TEXT NOT NULL DEFAULT '',
		revoked_at    TEXT NOT NULL DEFAULT '',
		created_at    TEXT NOT NULL DEFAULT ''
	)`,

	// 剪藏：幂等请求（临时数据，不参与备份，定时清理 24 小时前记录）
	`CREATE TABLE IF NOT EXISTS clip_requests (
		token_id   INTEGER NOT NULL,
		client_id  TEXT NOT NULL,
		item_id    INTEGER NOT NULL,
		created_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (token_id, client_id)
	)`,

	// 剪藏：正文快照（P1 使用，P0 建表以便备份/迁移口径一致）
	`CREATE TABLE IF NOT EXISTS clip_contents (
		item_id      INTEGER PRIMARY KEY,
		url_key      TEXT NOT NULL DEFAULT '',
		url_original TEXT NOT NULL DEFAULT '',
		source       TEXT NOT NULL DEFAULT '',
		excerpt      TEXT NOT NULL DEFAULT '',
		content_md   TEXT NOT NULL DEFAULT '',
		cover_path   TEXT NOT NULL DEFAULT '',
		fetched_at   TEXT NOT NULL DEFAULT '',
		http_status  INTEGER NOT NULL DEFAULT 0,
		content_hash TEXT NOT NULL DEFAULT '',
		word_count   INTEGER NOT NULL DEFAULT 0,
		fetch_error  TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
	)`,

	// 剪藏标签（P2）：一卡多标签，卡片删除时级联清理
	`CREATE TABLE IF NOT EXISTS item_tags (
		item_id INTEGER NOT NULL,
		tag     TEXT NOT NULL,
		PRIMARY KEY (item_id, tag),
		FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_item_tags_tag ON item_tags(tag)`,
}

// backfillParentIDs 孤儿分组清理：parent_id 指向不存在的 id 时重置为 0
func backfillParentIDs() {
	type pair struct{ id, pid int }
	var orphans []pair
	rows, err := DB.Query("SELECT id, parent_id FROM item_groups WHERE parent_id != 0")
	if err != nil {
		log.Printf("[db] 读取分组父子关系失败: %v", err)
		return
	}
	for rows.Next() {
		var p pair
		rows.Scan(&p.id, &p.pid)
		orphans = append(orphans, p)
	}
	rows.Close() // 必须在此关闭，释放读锁

	for _, p := range orphans {
		var cnt int
		DB.QueryRow("SELECT COUNT(*) FROM item_groups WHERE id=?", p.pid).Scan(&cnt)
		if cnt == 0 {
			DB.Exec("UPDATE item_groups SET parent_id=0 WHERE id=?", p.id)
		}
	}
}

// seedIfEmpty 首次安装写默认设置与管理员账号；已安装实例只补全缺失设置键
func seedIfEmpty() {
	defaults := settings.Defaults()

	var userCount int
	DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)

	if userCount == 0 {
		log.Println("[db] 首次安装，写入默认设置与管理员账号...")
		for k, v := range defaults {
			DB.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", k, v)
		}
		hashed, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		DB.Exec("INSERT INTO users (username, password, name, status, role) VALUES (?, ?, ?, ?, ?)",
			"admin", string(hashed), "管理员", 1, "admin")
		log.Println("[db] 初始化完成，管理员: admin / admin123")
	} else {
		var missing int
		for k, v := range defaults {
			var cnt int
			DB.QueryRow("SELECT COUNT(*) FROM settings WHERE config_name = ?", k).Scan(&cnt)
			if cnt == 0 {
				DB.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", k, v)
				missing++
			}
		}
		if missing > 0 {
			log.Printf("[db] 补全 %d 个缺失的设置键\n", missing)
		}
	}
}

// GetSetting 读单个设置，不存在返回空串
func GetSetting(key string) string {
	var v string
	err := DB.QueryRow("SELECT config_value FROM settings WHERE config_name = ?", key).Scan(&v)
	if err != nil {
		return ""
	}
	return v
}

// SetSetting 写入单个设置，不存在则插入
func SetSetting(key, value string) {
	res, err := DB.Exec("UPDATE settings SET config_value=? WHERE config_name=?", value, key)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		DB.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", key, value)
	}
}
