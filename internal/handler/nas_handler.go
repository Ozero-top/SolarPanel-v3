package handler

import (
	"net/http"
	"strconv"
	"strings"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
	"solarpanel/internal/nas"
)

// NASHandler 输出宿主机与 Docker 容器状态（面板开关与访客权限均读设置表）
type NASHandler struct {
	collector *nas.Collector
}

func NewNASHandler(cfg *config.Config) *NASHandler {
	return &NASHandler{collector: nas.NewCollector(cfg.NASHostRoot, cfg.DockerSocket)}
}

func (h *NASHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")

	settings := make(map[string]string, 5)
	rows, err := db.DB.Query(`SELECT config_name, config_value FROM settings
		WHERE config_name IN ('nas_show','nas_layout','nas_refresh','nas_guest','nas_disk_paths')`)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取设置失败")
		return
	}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			settings[k] = v
		}
	}
	rows.Close()

	if settings["nas_show"] != "1" {
		OK(w, map[string]any{"enabled": false})
		return
	}

	sess := auth.GetSessionFromRequest(r)
	isMember := sess != nil && sess.Role != "guest"
	if !isMember && settings["nas_guest"] != "1" {
		Err(w, http.StatusForbidden, 1, "未开启访客查看 NAS 状态")
		return
	}

	var diskPaths []string
	for _, p := range strings.Split(settings["nas_disk_paths"], ",") {
		if p = strings.TrimSpace(p); p != "" {
			diskPaths = append(diskPaths, p)
		}
	}

	refresh := 3
	if v, err := strconv.Atoi(settings["nas_refresh"]); err == nil && v >= 2 && v <= 60 {
		refresh = v
	}

	OK(w, map[string]any{
		"enabled":  true,
		"layout":   settings["nas_layout"],
		"refresh":  refresh,
		"snapshot": h.collector.Collect(diskPaths),
	})
}
