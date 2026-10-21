package db

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"strings"
	"time"

	"solarpanel/internal/config"
	"solarpanel/internal/settings"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed data/seed_groups.json
var seedFS embed.FS

// seedGroupEntry 对应 data/seed_groups.json 的每项
type seedGroupEntry struct {
	Name        string `json:"name"`
	Parent      string `json:"parent"`
	Description string `json:"description"`
	Items       []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Logo        string `json:"logo"`
	} `json:"items"`
}

var DB *sql.DB

// Init 初始化 SQLite + 建表 + 迁移 + 种子
func Init(cfg *config.Config) error {
	var err error
	DB, err = sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return err
	}
	// SQLite WAL 模式下推荐 10-25；过小会导致并发请求竞争连接而卡住
	DB.SetMaxOpenConns(15)
	// 连接闲置后关闭释放文件句柄（SQLite 文件锁场景下有用）
	DB.SetConnMaxIdleTime(5 * time.Minute)

	// WAL 模式（Write-Ahead Logging）：一个写 + 多个读并发，journal_mode=DELETE 会锁死所有读
	DB.Exec("PRAGMA journal_mode=WAL")
	DB.Exec("PRAGMA synchronous=NORMAL")    // WAL 下 NORMAL 足够安全，性能更好
	DB.Exec("PRAGMA busy_timeout=15000")    // 15s：并发写竞争时给够等待窗口
	DB.Exec("PRAGMA foreign_keys=ON")
	DB.Exec("PRAGMA cache_size=-20000")     // 20MB page cache（负号表示 KB）
	DB.Exec("PRAGMA temp_store=MEMORY")     // 临时表/索引走内存，减少磁盘 IO

	log.Println("[db] SQLite connected:", cfg.DBPath)

	// 执行建表
	for i, ddl := range schemaDDL {
		if _, err = DB.Exec(ddl); err != nil {
			log.Printf("[db] DDL[%d] err: %v\n  %s\n", i, err, ddl)
		} else {
			log.Printf("[db] DDL[%d] OK", i)
		}
	}

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
		created_at TEXT NOT NULL DEFAULT ''
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
}

// backfillParentIDs 回填孤儿分组的 parent_id
// v1 迁移只造虚拟父，种子注入后子分组可能 parent_id=0 但 title 匹配虚拟父，需要重挂
func backfillParentIDs() {
	type mapping struct {
		parentTitle string
		children    []string
	}
	mappings := []mapping{
		{"灵感采集", []string{"发现产品", "界面灵感", "网页灵感"}},
		{"素材资源", []string{"图标素材", "LOGO设计", "平面素材", "UI资源", "Sketch资源", "字体资源", "Mockup", "摄影图库", "PPT资源"}},
		{"常用工具", []string{"图形创意", "界面设计", "交互动效", "在线配色", "在线工具", "Chrome插件"}},
		{"学习教程", []string{"设计规范", "视频教程", "设计文章", "设计电台", "交互设计"}},
	}

	for _, m := range mappings {
		var parentID int
		err := DB.QueryRow("SELECT id FROM item_groups WHERE title=? AND parent_id=0 LIMIT 1", m.parentTitle).Scan(&parentID)
		if err != nil {
			// 虚拟父不存在 → 造一个
			res, _ := DB.Exec("INSERT INTO item_groups (parent_id, title, description, sort, is_visible, user_id) VALUES (0, ?, '', 999, 1, 1)",
				m.parentTitle)
			pid, _ := res.LastInsertId()
			parentID = int(pid)
		}
		// 子分组 title 匹配 → 重挂到这个 parent
		for _, c := range m.children {
			DB.Exec("UPDATE item_groups SET parent_id=? WHERE title=? AND parent_id=0", parentID, c)
		}
	}

	// 孤儿扫描：parent_id 指向不存在的 id → 重置为 0
	// 注意：SetMaxOpenConns(1) 下 rows 未 Close 时不能再嵌套 QueryRow，否则死锁！
	type pair struct{ id, pid int }
	var orphans []pair
	rows, _ := DB.Query("SELECT id, parent_id FROM item_groups WHERE parent_id != 0")
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

// SeedPresetGroupsAndItems 从 data/seed_groups.json 注入完整预置分组 + 卡片
// 幂等：判"常用推荐"顶级分组存在即视为种子完整，否则清光重造
func SeedPresetGroupsAndItems() {
	// 种子完整性判定
	var presetOK int
	DB.QueryRow("SELECT COUNT(*) FROM item_groups WHERE title='常用推荐' AND parent_id=0").Scan(&presetOK)
	if presetOK > 0 {
		log.Println("[db] 预置种子已完整，跳过")
		return
	}

	// 种子残缺 → 清光重造（items 依赖 item_groups，先清 items 再清 groups）
	DB.Exec("DELETE FROM items")
	DB.Exec("DELETE FROM item_groups")

	data, err := fs.ReadFile(seedFS, "data/seed_groups.json")
	if err != nil {
		log.Printf("[db] seed_groups.json 读取失败: %v\n", err)
		return
	}

	var entries []seedGroupEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("[db] seed_groups.json 解析失败: %v\n", err)
		return
	}

	var adminID int
	DB.QueryRow("SELECT id FROM users WHERE role='admin' LIMIT 1").Scan(&adminID)
	if adminID == 0 {
		adminID = 1
	}

	// 第一步：造所有虚拟父（parent 字段指向但自己不是顶级的那些）
	// seed_groups.json 里的 parent 引用了 "灵感采集" / "素材资源" / "常用工具" / "学习教程"
	// 这些是虚拟父，在 JSON 里没有独立条目，需要我们造
	virtualParents := []string{"灵感采集", "素材资源", "常用工具", "学习教程", "社区资讯", "UED团队"}
	parentIDMap := map[string]int{}
	for _, vp := range virtualParents {
		res, _ := DB.Exec("INSERT INTO item_groups (parent_id, title, description, sort, is_visible, user_id) VALUES (0, ?, '', 999, 1, ?)",
			vp, adminID)
		pid, _ := res.LastInsertId()
		parentIDMap[vp] = int(pid)
	}

	// 第二步：造 JSON 里的每个分组（顶级 parent="" / 子分组 parent="虚拟父名"）
	sort := 0
	groupIDMap := map[string]int{}
	for _, e := range entries {
		pid := 0
		if e.Parent != "" {
			if v, ok := parentIDMap[e.Parent]; ok {
				pid = v
			}
		}
		res, err := DB.Exec("INSERT INTO item_groups (parent_id, title, icon, description, sort, is_visible, user_id) VALUES (?, ?, '', ?, 1, ?)",
			pid, e.Name, e.Description, sort, adminID)
		if err != nil {
			log.Printf("[db] seed group '%s': %v\n", e.Name, err)
			continue
		}
		gid, _ := res.LastInsertId()
		groupIDMap[e.Name] = int(gid)
		sort++
	}

	// 第三步：造每个分组下的卡片
	itemSort := 0
	for _, e := range entries {
		gid, ok := groupIDMap[e.Name]
		if !ok {
			continue
		}
		for _, it := range e.Items {
			iconType := "favicon"
			if strings.HasSuffix(it.Logo, ".svg") || it.Logo == "" {
				iconType = "favicon"
			}
			_, err := DB.Exec(`INSERT INTO items
				(group_id, title, url, description, icon_type, icon_value, open_method, sort, user_id)
				VALUES (?, ?, ?, ?, ?, ?, 2, ?, ?)`,
				gid, it.Title, it.URL, it.Description, iconType, it.Logo, itemSort, adminID)
			if err != nil {
				log.Printf("[db] seed item '%s': %v\n", it.Title, err)
			}
			itemSort++
		}
	}

	// 第四步：再跑一次 backfillParentIDs 保证孤儿子分组也挂到正确父
	backfillParentIDs()

	var groups, items int
	DB.QueryRow("SELECT COUNT(*) FROM item_groups").Scan(&groups)
	DB.QueryRow("SELECT COUNT(*) FROM items").Scan(&items)
	log.Printf("[db] ✅ 预置种子注入完成：%d 分组 + %d 卡片\n", groups, items)
}

// seedIfEmpty 首次安装写种子；已安装实例补全缺失设置键 + 兜底注入预置分组卡片
func seedIfEmpty() {
	defaults := settings.Defaults()

	var userCount int
	DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)

	if userCount == 0 {
		log.Println("[db] 首次安装，写入种子数据...")
		for k, v := range defaults {
			DB.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", k, v)
		}
		hashed, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		DB.Exec("INSERT INTO users (username, password, name, status, role) VALUES (?, ?, ?, ?, ?)",
			"admin", string(hashed), "管理员", 1, "admin")

		SeedPresetGroupsAndItems()
		log.Println("[db] 种子数据写入完成，管理员: admin / admin123")
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
		// 兜底：Reset 清光后重启 → presetOK==0 时注入
		var presetOK int
		DB.QueryRow("SELECT COUNT(*) FROM item_groups WHERE title='常用推荐' AND parent_id=0").Scan(&presetOK)
		if presetOK == 0 {
			SeedPresetGroupsAndItems()
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

// GetSettingDefault 读单个设置，不存在返回指定默认值
func GetSettingDefault(key, def string) string {
	v := GetSetting(key)
	if v == "" {
		return def
	}
	return v
}

// SetSetting 写单个设置（用于非 HTTP 上下文）
func SetSetting(key, value string) {
	DB.Exec(`INSERT INTO settings (config_name, config_value) VALUES (?, ?)
		ON CONFLICT(config_name) DO UPDATE SET config_value=excluded.config_value, updated_at=?`,
		key, value, NowStr())
}
