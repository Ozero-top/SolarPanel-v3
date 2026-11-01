package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
	"solarpanel/internal/nas"
)

var nasSettingsKeys = []string{"nas_show", "nas_layout", "nas_refresh", "nas_guest", "nas_disk_paths"}

var nasAlertKeys = []string{
	"nas_show", "nas_disk_paths", "nas_refresh",
	"nas_alert_enable", "nas_alert_disk", "nas_alert_temp", "nas_alert_webhook",
}

// alertEntry 去重记录：同一 key 同一 level 6 小时内不重复推送
type alertEntry struct {
	level string
	at    time.Time
}

// alertMsg 待推送的告警消息
type alertMsg struct {
	level, title, content string
}

// NASHandler 输出宿主机与 Docker 容器状态（面板开关与访客权限均读设置表）
type NASHandler struct {
	collector *nas.Collector

	alertMu sync.Mutex
	alerts  map[string]alertEntry
	running map[string]bool // 上一轮运行中的容器
	down    map[string]bool // 已发现停止、尚未恢复的容器
}

func NewNASHandler(cfg *config.Config) *NASHandler {
	h := &NASHandler{
		collector: nas.NewCollector(cfg.NASHostRoot, cfg.DockerSocket, filepath.Dir(cfg.DBPath)),
		alerts:    make(map[string]alertEntry),
		running:   make(map[string]bool),
		down:      make(map[string]bool),
	}
	go h.alertLoop()
	return h
}

// loadNASSettings 批量读取设置
func loadNASSettings(keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		args = append(args, k)
	}
	rows, err := db.DB.Query(`SELECT config_name, config_value FROM settings
		WHERE config_name IN (`+placeholders+`)`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out, rows.Err()
}

func nasRefresh(settings map[string]string) int {
	if v, err := strconv.Atoi(settings["nas_refresh"]); err == nil && v >= 2 && v <= 60 {
		return v
	}
	return 3
}

// nasTTL 采集缓存时长：不超过 3s，且不小于刷新间隔（最小 2s）
func nasTTL(settings map[string]string) time.Duration {
	return time.Duration(min(nasRefresh(settings), 3)) * time.Second
}

func nasDiskPaths(settings map[string]string) []string {
	var paths []string
	for _, p := range strings.Split(settings["nas_disk_paths"], ",") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func (h *NASHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, max-age=0")

	settings, err := loadNASSettings(nasSettingsKeys...)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "读取设置失败")
		return
	}

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

	snap := h.collector.Collect(nasDiskPaths(settings), nasTTL(settings))
	// 访客脱敏：仅开启 nas_guest 时生效，成员/管理员不受影响
	if !isMember && settings["nas_guest"] == "1" {
		maskSnapshot(&snap)
	}

	OK(w, map[string]any{
		"enabled":  true,
		"layout":   settings["nas_layout"],
		"refresh":  nasRefresh(settings),
		"snapshot": snap,
	})
}

// maskSnapshot 访客脱敏（在快照副本上重建切片，不影响缓存给成员的数据）
func maskSnapshot(s *nas.Snapshot) {
	s.Host.IP = ""
	s.Host.Hostname = "NAS"
	s.Host.Temps = []nas.TempInfo{}
	s.Host.Fans = []nas.FanInfo{}
	s.Containers = []nas.ContainerInfo{}
	disks := make([]nas.DiskInfo, 0, len(s.Disks))
	for i, d := range s.Disks {
		d.Mount = fmt.Sprintf("存储池 %d", i+1)
		d.FSType = ""
		disks = append(disks, d)
	}
	s.Disks = disks
	s.HostRoot = ""
	s.GuestMasked = true
}

/* ================= 阈值告警 ================= */

func (h *NASHandler) alertLoop() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		h.checkAlerts()
	}
}

func (h *NASHandler) resetAlertState() {
	h.alertMu.Lock()
	h.alerts = make(map[string]alertEntry)
	h.running = make(map[string]bool)
	h.down = make(map[string]bool)
	h.alertMu.Unlock()
}

func (h *NASHandler) checkAlerts() {
	settings, err := loadNASSettings(nasAlertKeys...)
	if err != nil {
		return
	}
	if settings["nas_alert_enable"] != "1" || strings.TrimSpace(settings["nas_alert_webhook"]) == "" {
		h.resetAlertState()
		return
	}
	webhook := strings.TrimSpace(settings["nas_alert_webhook"])

	diskLimit, _ := strconv.ParseFloat(settings["nas_alert_disk"], 64)
	if diskLimit <= 0 {
		diskLimit = 90
	}
	tempLimit, _ := strconv.ParseFloat(settings["nas_alert_temp"], 64)
	if tempLimit <= 0 {
		tempLimit = 75
	}

	snap := h.collector.Collect(nasDiskPaths(settings), nasTTL(settings))

	conditions := make(map[string]alertMsg)
	for _, d := range snap.Disks {
		if d.Total == 0 {
			continue
		}
		pct := float64(d.Used) / float64(d.Total) * 100
		if pct < diskLimit {
			continue
		}
		level := "warn"
		if pct >= 95 {
			level = "danger"
		}
		conditions["disk:"+d.Mount] = alertMsg{
			level:   level,
			title:   "NAS 存储池告警",
			content: fmt.Sprintf("存储池 %s 使用率 %.1f%%（阈值 %.0f%%）", d.Mount, pct, diskLimit),
		}
	}
	if snap.Host.TempC >= 0 && snap.Host.TempC >= tempLimit {
		name := snap.Host.TempName
		if name == "" {
			name = "CPU"
		}
		conditions["temp"] = alertMsg{
			level:   "warn",
			title:   "NAS 温度告警",
			content: fmt.Sprintf("%s 温度 %.1f°C（阈值 %.0f°C）", name, snap.Host.TempC, tempLimit),
		}
	}

	// 容器：之前 running、现在不在 running 视为异常，直到重新运行才恢复
	if snap.DockerOK {
		running := make(map[string]bool, len(snap.Containers))
		for _, ct := range snap.Containers {
			if ct.State == "running" {
				running[ct.Name] = true
			}
		}
		h.alertMu.Lock()
		for name := range h.running {
			if !running[name] {
				h.down[name] = true
			}
		}
		for name := range running {
			delete(h.down, name)
		}
		h.running = running
		for name := range h.down {
			conditions["ct:"+name] = alertMsg{
				level:   "warn",
				title:   "NAS 容器告警",
				content: fmt.Sprintf("容器 %s 已停止运行", name),
			}
		}
		h.alertMu.Unlock()
	}

	now := time.Now()
	var out []alertMsg
	// conditions 与去重表在 alertMu 下更新，避免与 resetAlertState 并发写 map
	h.alertMu.Lock()
	for key, c := range conditions {
		prev, ok := h.alerts[key]
		if ok && prev.level == c.level && now.Sub(prev.at) < 6*time.Hour {
			continue
		}
		out = append(out, c)
		h.alerts[key] = alertEntry{level: c.level, at: now}
	}
	for key := range h.alerts {
		if _, ok := conditions[key]; ok {
			continue
		}
		out = append(out, alertMsg{
			level:   "ok",
			title:   "NAS 告警恢复",
			content: recoverMessage(key),
		})
		delete(h.alerts, key)
	}
	host := snap.Host.Hostname
	h.alertMu.Unlock()

	for _, m := range out {
		if err := sendWebhook(webhook, m.title, m.content, m.level, host); err != nil {
			log.Printf("[nas] 告警推送失败: %v", err)
		}
	}
}

func recoverMessage(key string) string {
	switch {
	case strings.HasPrefix(key, "disk:"):
		return "存储池 " + strings.TrimPrefix(key, "disk:") + " 使用率已恢复正常"
	case key == "temp":
		return "温度已恢复正常"
	case strings.HasPrefix(key, "ct:"):
		return "容器 " + strings.TrimPrefix(key, "ct:") + " 已恢复运行"
	}
	return "告警条件已恢复"
}

// sendWebhook 推送告警；飞书/Lark 使用文本消息格式，其余使用通用 JSON
func sendWebhook(webhook, title, content, level, host string) error {
	var payload any
	if u, err := url.Parse(webhook); err == nil && isLarkHost(u.Hostname()) {
		payload = map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": title + "\n" + content},
		}
	} else {
		payload = map[string]any{
			"title":   title,
			"content": content,
			"level":   level,
			"host":    host,
			"time":    time.Now().Format("2006-01-02 15:04:05"),
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(webhook, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func isLarkHost(host string) bool {
	h := strings.ToLower(host)
	for _, k := range []string{"feishu", "lark", "larksuite"} {
		if strings.Contains(h, k) {
			return true
		}
	}
	return false
}

// Dispatch 处理 POST /api/nas.php?action=test_alert（仅管理员）
func (h *NASHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || sess.Role != "admin" {
		Err(w, http.StatusForbidden, 1, "仅管理员可操作")
		return
	}
	if GetQuery(r, "action", "") != "test_alert" {
		Fail(w, "未知操作")
		return
	}

	settings, err := loadNASSettings("nas_alert_webhook")
	if err != nil {
		Fail(w, "读取设置失败")
		return
	}
	webhook := strings.TrimSpace(settings["nas_alert_webhook"])
	if webhook == "" {
		Fail(w, "未配置告警 Webhook")
		return
	}

	host, _ := os.Hostname()
	if err := sendWebhook(webhook, "SolarPanel 测试告警", "这是一条来自 SolarPanel 的测试告警，收到即表示 Webhook 配置成功。", "ok", host); err != nil {
		Fail(w, "发送失败："+err.Error())
		return
	}
	OK(w, map[string]any{"sent": true})
}
