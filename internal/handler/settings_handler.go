package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"

	"golang.org/x/crypto/bcrypt"
)

type SettingsHandler struct{}

var allowedSettingKeys = map[string]struct{}{
	"site_title": {}, "site_logo": {}, "wallpaper": {}, "mask_opacity": {},
	"wallpaper_blur": {}, "announcement": {}, "announcement_show": {},
	"footer": {}, "clock_show": {}, "clock_font": {}, "page_font": {}, "default_theme": {}, "theme_style": {},
	"card_style": {}, "card_opacity": {}, "default_lan_mode": {}, "lan_hostnames": {},
	"default_group": {},
	"wetab_prefs": {}, "wetab_dock": {},
	"search_engines": {}, "search_default": {}, "site_url": {},
	"content_maxwidth": {}, "content_pad_lr": {}, "content_pad_top": {},
	"content_pad_bottom": {}, "weather_show": {}, "weather_city": {},
	"search_width": {}, "news_show": {}, "news_sources": {}, "news_order": {}, "news_auto_refresh": {},
	"nas_show": {}, "nas_layout": {}, "nas_refresh": {}, "nas_guest": {}, "nas_disk_paths": {}, "nas_modal_alpha": {},
	"icp_show": {}, "icp_number": {}, "icp_link": {}, "police_show": {},
	"police_number": {}, "police_link": {}, "wallpaper_source": {},
	"guest_access_enabled": {}, "guest_password_hash": {},
	"search_bar_enabled": {}, "topbar_desktop_enabled": {},
	"topbar_mobile_enabled": {},
	"update_check":          {}, "update_check_interval": {},
	"auto_backup_enabled": {}, "auto_backup_interval": {},
	"clip_enabled": {}, "clip_default_group": {}, "clip_default_capture": {},
	"clip_ai_enabled": {}, "clip_ai_provider": {}, "clip_ai_base_url": {},
	"clip_ai_api_key": {}, "clip_ai_model": {}, "clip_image_domains": {},
}

// settingValueWhitelist 特定键的取值白名单（与 frontend/themes.json / card_style 可选项保持同步）
var settingValueWhitelist = map[string]map[string]bool{
	"theme_style":          {"soft": true, "blueprint": true, "clarity": true, "soft-utility": true, "aurora": true, "iris": true, "sketch-style": true, "retro-vintage": true, "corporate-clean": true, "liquid-glass": true, "neo-brutalist": true, "wetab": true},
	"card_style":           {"nav": true, "navapp": true},
	"default_lan_mode":     {"auto": true, "public": true, "lan": true},
	"nas_show":             {"0": true, "1": true},
	"nas_guest":            {"0": true, "1": true},
	"news_auto_refresh":    {"0": true, "1": true},
	"update_check":         {"0": true, "1": true},
	"nas_layout":           {"right": true, "left": true, "both": true, "banner": true},
	"auto_backup_enabled":  {"0": true, "1": true},
	"auto_backup_interval": {"1": true, "3": true, "5": true, "7": true, "9": true},
	"clip_enabled":         {"0": true, "1": true},
	"clip_default_capture": {"card": true, "snapshot": true},
	"clip_ai_enabled":      {"0": true, "1": true},
	"clip_ai_provider":     {"local": true, "api": true},
	// 时钟/天气字体（与 frontend/assets/js/api.js SP_CLOCK_FONTS 保持一致；空 = 跟随系统）
	"clock_font": {
		"":         true,
		"orbitron": true, "rajdhani": true, "audiowide": true, "michroma": true,
		"bebas": true, "poppins": true, "montserrat": true, "playfair": true,
		"righteous": true, "spacegrotesk": true, "jetbrains": true, "rubik": true,
		"oxanium": true, "chakrapetch": true, "sharetechmono": true,
		"teko": true, "exo2": true, "syncopate": true,
	},
	// 整页字体（与 frontend/assets/js/api.js SP_PAGE_FONTS 保持一致；空 = 跟随系统）
	"page_font": {
		"":         true,
		"notosans": true, "notoserif": true, "zcoolxiaowei": true, "zcoolqingke": true,
		"zcoolkuaile": true, "mashanzheng": true, "longcang": true, "zhimangxing": true,
		"poppins": true, "montserrat": true, "orbitron": true, "jetbrains": true,
		"liujianmaocao": true, "lxgwwenkai": true,
		"inter": true, "lora": true, "nunito": true, "firacode": true,
	},
}

// SettingRow 设置行
type SettingRow struct {
	ID          int    `json:"id"`
	ConfigName  string `json:"config_name"`
	ConfigValue string `json:"config_value"`
}

func (h *SettingsHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	action := GetQuery(r, "action", "")
	method := r.Method

	if method == "POST" {
		switch action {
		case "save":
			h.Save(w, r)
		default:
			Fail(w, "未知操作")
		}
		return
	}

	if action == "list" || method == "GET" {
		h.List(w, r)
		return
	}

	Fail(w, "未知操作")
}

func (h *SettingsHandler) List(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	isAdmin := sess != nil && sess.Role == "admin"

	rows, err := db.DB.Query("SELECT id, config_name, config_value FROM settings")
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取设置失败")
		return
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var row SettingRow
		rows.Scan(&row.ID, &row.ConfigName, &row.ConfigValue)
		if !isAdmin && sensitiveSettingKeys[row.ConfigName] {
			continue
		}
		m[row.ConfigName] = row.ConfigValue
	}
	OK(w, m)
}

func (h *SettingsHandler) Save(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅管理员可修改站点设置")
		return
	}

	var body map[string]string
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}

	saved := 0
	for k, v := range body {
		if _, ok := allowedSettingKeys[k]; !ok {
			continue
		}
		// 值白名单校验：不在允许值内的直接跳过（防写入已废弃主题/样式）
		if vals, ok := settingValueWhitelist[k]; ok && !vals[v] {
			continue
		}
		if k == "nas_modal_alpha" {
			n, err := strconv.Atoi(v)
			if err != nil {
				continue
			}
			if n < 40 {
				n = 40
			} else if n > 100 {
				n = 100
			}
			v = strconv.Itoa(n)
		}
		// 剪藏默认分组：0 表示自动创建「稍后读」，其余必须是可见分组
		if k == "clip_default_group" {
			n, err := strconv.Atoi(v)
			if err != nil {
				continue
			}
			if n < 0 {
				n = 0
			}
			if n > 0 {
				var exists int
				if err := db.DB.QueryRow("SELECT COUNT(*) FROM item_groups WHERE id = ? AND is_visible = 1", n).Scan(&exists); err != nil || exists == 0 {
					continue
				}
			}
			v = strconv.Itoa(n)
		}
		if k == "update_check_interval" {
			n, err := strconv.Atoi(v)
			if err != nil {
				continue
			}
			if n < 1 {
				n = 1
			} else if n > 24 {
				n = 24
			}
			v = strconv.Itoa(n)
		}
		// 外部 AI 接口地址/密钥/模型：去空格并限制长度，地址必须为 http(s)
		if k == "clip_ai_base_url" {
			v = strings.TrimRight(strings.TrimSpace(v), "/")
			if len(v) > 300 {
				continue
			}
			if v != "" {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					continue
				}
			}
		}
		if k == "clip_ai_api_key" && len(strings.TrimSpace(v)) > 300 {
			continue
		}
		if k == "clip_ai_model" {
			v = strings.TrimSpace(v)
			if len(v) > 80 {
				continue
			}
		}
		if k == "clip_image_domains" && len(v) > 2000 {
			continue
		}
		if k == "guest_password_hash" {
			if v == "" {
				continue
			}
			hashed, err := bcrypt.GenerateFromPassword([]byte(v), bcrypt.DefaultCost)
			if err != nil {
				continue
			}
			v = string(hashed)
		}
		res, err := db.DB.Exec("UPDATE settings SET config_value=? WHERE config_name=?", v, k)
		if err != nil {
			continue
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			db.DB.Exec("INSERT INTO settings (config_name, config_value) VALUES (?, ?)", k, v)
			saved++
		} else {
			saved++
		}
	}

	if saved > 0 {
		auth.AuditLog(w, r, "settings.save",
			"保存 "+strconv.Itoa(saved)+" 项", "success",
			usernameByUID(sess.UID), sess.Role, "")
	}
	OK(w, map[string]int{"saved": saved})
}

func usernameByUID(uid uint) string {
	if uid == 0 {
		return "访客"
	}
	var username string
	db.DB.QueryRow("SELECT username FROM users WHERE id=?", uid).Scan(&username)
	return username
}
