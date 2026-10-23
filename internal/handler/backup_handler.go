package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
	"solarpanel/internal/settings"
)

type BackupHandler struct {
	cfg *config.Config
}

func NewBackupHandler(cfg *config.Config) *BackupHandler {
	return &BackupHandler{cfg: cfg}
}

type backupPayload struct {
	Version  string            `json:"version"`
	Time     string            `json:"time"`
	Settings []SettingRow      `json:"settings"`
	Groups   []ItemGroupRow    `json:"groups"`
	Items    []ItemRow         `json:"items"`
	Feeds    []CustomFeedRow   `json:"feeds"`
	Files    map[string]string `json:"files"`
}

const backupMaxFileSize = 20 * 1024 * 1024

// backupAllowedFileExt 备份只允许还原图片类资源，避免任意文件写入上传目录
var backupAllowedFileExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"webp": true, "bmp": true, "ico": true, "svg": true,
}

func (h *BackupHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	action := r.URL.Query().Get("action")

	if method == "GET" {
		h.Export(w, r)
		return
	}
	if method == "POST" {
		switch action {
		case "import":
			h.Import(w, r)
		case "reset":
			h.Reset(w, r)
		default:
			Fail(w, "未知操作")
		}
		return
	}
	Fail(w, "不支持的请求方法")
}

func (h *BackupHandler) Export(w http.ResponseWriter, r *http.Request) {
	// 备份包含全部设置（含访客密码哈希）与上传资源，仅管理员可导出
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅管理员可导出备份")
		return
	}

	settingQ, err := db.DB.Query("SELECT id, config_name, config_value FROM settings")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取设置失败")
		return
	}
	var settings []SettingRow
	for settingQ.Next() {
		var s SettingRow
		settingQ.Scan(&s.ID, &s.ConfigName, &s.ConfigValue)
		settings = append(settings, s)
	}
	settingQ.Close()

	groupQ, err := db.DB.Query("SELECT id, parent_id, title, icon, description, sort, is_visible, user_id, created_at FROM item_groups ORDER BY parent_id ASC, sort ASC, id ASC")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取分组失败")
		return
	}
	var groups []ItemGroupRow
	for groupQ.Next() {
		var g ItemGroupRow
		groupQ.Scan(&g.ID, &g.ParentID, &g.Title, &g.Icon, &g.Description, &g.Sort, &g.IsVisible, &g.UserID, &g.CreatedAt)
		groups = append(groups, g)
	}
	groupQ.Close()

	itemQ, err := db.DB.Query("SELECT id, group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id, created_at FROM items")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取卡片失败")
		return
	}
	var items []ItemRow
	for itemQ.Next() {
		var it ItemRow
		itemQ.Scan(&it.ID, &it.GroupID, &it.Title, &it.URL, &it.LanURL, &it.Description, &it.IconType, &it.IconValue, &it.IconBG, &it.OpenMethod, &it.Sort, &it.IsHidden, &it.UserID, &it.CreatedAt)
		items = append(items, it)
	}
	itemQ.Close()

	feedQ, err := db.DB.Query("SELECT id, title, url, sort, enabled, created_at FROM custom_feeds ORDER BY sort ASC, id ASC")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取 RSS 源失败")
		return
	}
	var feeds []CustomFeedRow
	for feedQ.Next() {
		var f CustomFeedRow
		feedQ.Scan(&f.ID, &f.Title, &f.URL, &f.Sort, &f.Enabled, &f.CreatedAt)
		feeds = append(feeds, f)
	}
	feedQ.Close()

	files := make(map[string]string)
	skipDirs := map[string]bool{"weather": true}

	err = filepath.Walk(h.cfg.UploadDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(h.cfg.UploadDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		topDir := strings.SplitN(rel, "/", 2)[0]
		if skipDirs[topDir] {
			return nil
		}
		if info.Size() > backupMaxFileSize {
			log.Printf("[backup] 跳过过大文件: %s (%d bytes)", rel, info.Size())
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[backup] 读取文件失败: %s: %v", rel, err)
			return nil
		}
		files[rel] = base64.StdEncoding.EncodeToString(data)
		return nil
	})
	if err != nil {
		log.Printf("[backup] 遍历上传目录失败: %v", err)
	}

	now := time.Now()
	payload := backupPayload{
		Version: "1.0", Time: now.Format("2006-01-02 15:04:05"),
		Settings: settings, Groups: groups, Items: items, Feeds: feeds, Files: files,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "序列化失败")
		return
	}

	filename := fmt.Sprintf("solarpanel-backup-%s.json", now.Format("20060102-150405"))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(raw)
}

func (h *BackupHandler) Import(w http.ResponseWriter, r *http.Request) {
	// 导入会整体替换设置/分组/卡片并写入上传目录，仅管理员可执行
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅管理员可导入备份")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 128<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		file, _, err = r.FormFile("backup")
	}
	if err != nil {
		Fail(w, "未找到备份文件: "+err.Error())
		return
	}
	defer file.Close()

	body, err := io.ReadAll(file)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取文件失败")
		return
	}

	text := strings.TrimSpace(string(body))
	var payload backupPayload

	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
		if err := json.Unmarshal(decoded, &payload); err != nil {
			Fail(w, "备份文件内容不是有效的 JSON")
			return
		}
	} else {
		if err := json.Unmarshal(body, &payload); err != nil {
			Fail(w, "备份文件格式无效（base64 或纯 JSON）")
			return
		}
	}

	tx, err := db.DB.Begin()
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "导入失败：无法开启事务")
		return
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM items")
	tx.Exec("DELETE FROM item_groups")
	tx.Exec("DELETE FROM custom_feeds")
	tx.Exec("DELETE FROM settings")

	for _, s := range payload.Settings {
		tx.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", s.ConfigName, s.ConfigValue)
	}

	// 关键：按 parent_id ASC 排序，确保父分组先于子分组被插入，此时 groupIDMap 已有父分组映射
	sort.SliceStable(payload.Groups, func(i, j int) bool {
		if payload.Groups[i].ParentID != payload.Groups[j].ParentID {
			return payload.Groups[i].ParentID < payload.Groups[j].ParentID
		}
		return payload.Groups[i].ID < payload.Groups[j].ID
	})

	var groupIDMap = make(map[int64]int64)
	for _, g := range payload.Groups {
		// 重映射 parent_id：备份中的老父分组 id 重新插入后变成新 id，必须通过 groupIDMap 转换
		// 备份里 groups 按 parent_id ASC 排序，父分组先处理，此时 groupIDMap 一定已有父分组映射
		newParentID := g.ParentID
		if g.ParentID != 0 {
			if mapped, ok := groupIDMap[int64(g.ParentID)]; ok {
				newParentID = int(mapped)
			} else {
				log.Printf("[backup] 警告：子分组 '%s' (old_id=%d) 的父分组 old_id=%d 未找到映射，降级为顶级",
					g.Title, g.ID, g.ParentID)
				newParentID = 0
			}
		}
		res, err := tx.Exec("INSERT INTO item_groups (parent_id, title, icon, description, sort, is_visible, user_id) VALUES (?, ?, ?, ?, ?, ?, ?)",
			newParentID, g.Title, sanitizeGroupIcon(g.Icon), g.Description, g.Sort, g.IsVisible, 1)
		if err != nil {
			log.Printf("[backup] 导入分组 '%s' 失败: %v", g.Title, err)
			continue
		}
		newID, _ := res.LastInsertId()
		groupIDMap[int64(g.ID)] = newID
	}

	for _, it := range payload.Items {
		newGroupID, ok := groupIDMap[int64(it.GroupID)]
		if !ok {
			continue
		}
		tx.Exec("INSERT INTO items (group_id, title, url, lan_url, description, icon_type, icon_value, icon_bg, open_method, sort, is_hidden, user_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
			newGroupID, it.Title, it.URL, it.LanURL, it.Description, it.IconType, it.IconValue, it.IconBG, it.OpenMethod, it.Sort, it.IsHidden, 1)
	}

	for _, f := range payload.Feeds {
		tx.Exec("INSERT INTO custom_feeds (title, url, sort, enabled) VALUES (?, ?, ?, ?)", f.Title, f.URL, f.Sort, f.Enabled)
	}

	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, 1, "导入失败: "+err.Error())
		return
	}

	writtenFiles := 0
	if len(payload.Files) > 0 {
		skipPrefixes := []string{"weather/", "weather\\"}
		for relPath, b64Content := range payload.Files {
			relPath = filepath.ToSlash(relPath)
			skip := false
			for _, p := range skipPrefixes {
				if strings.HasPrefix(relPath, p) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			if strings.Contains(relPath, "..") {
				continue
			}
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(relPath), "."))
			if !backupAllowedFileExt[ext] {
				continue
			}
			absPath := filepath.Join(h.cfg.UploadDir, filepath.FromSlash(relPath))
			if !strings.HasPrefix(filepath.Clean(absPath), h.cfg.UploadDir) {
				continue
			}
			content, err := base64.StdEncoding.DecodeString(b64Content)
			if err != nil {
				continue
			}
			if int64(len(content)) > backupMaxFileSize {
				continue
			}
			if ext == "svg" {
				content = sanitizeSVG(content)
			}
			if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
				continue
			}
			if err := os.WriteFile(absPath, content, 0644); err != nil {
				continue
			}
			writtenFiles++
		}
	}

	OK(w, map[string]int{
		"settings": len(payload.Settings),
		"groups":   len(payload.Groups),
		"items":    len(payload.Items),
		"feeds":    len(payload.Feeds),
		"files":    writtenFiles,
	})
}

func (h *BackupHandler) Reset(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil {
		Err(w, http.StatusUnauthorized, 1, "未登录")
		return
	}
	if sess.Role != "admin" {
		Fail(w, "仅管理员可执行重置")
		return
	}

	tx, err := db.DB.Begin()
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "重置失败：无法开启事务")
		return
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM items")
	tx.Exec("DELETE FROM item_groups")
	tx.Exec("DELETE FROM custom_feeds")
	tx.Exec("DELETE FROM settings")

	defaults := settings.Defaults()
	for k, v := range defaults {
		tx.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", k, v)
	}

	if err := tx.Commit().Error; err != nil {
		Err(w, http.StatusInternalServerError, 1, "重置失败")
		return
	}

	h.clearUploadDirPreserveWeather()
	OK(w, nil)
}

func (h *BackupHandler) clearUploadDirPreserveWeather() {
	entries, err := os.ReadDir(h.cfg.UploadDir)
	if err != nil {
		log.Printf("[backup] 读取上传目录失败: %v", err)
		return
	}
	for _, e := range entries {
		if e.Name() == "weather" {
			continue
		}
		fullPath := filepath.Join(h.cfg.UploadDir, e.Name())
		if err := os.RemoveAll(fullPath); err != nil {
			log.Printf("[backup] 删除 %s 失败: %v", fullPath, err)
		}
	}
}
