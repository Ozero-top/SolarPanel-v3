package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
)

type GalleryHandler struct {
	cfg *config.Config
}

func NewGalleryHandler(cfg *config.Config) *GalleryHandler {
	return &GalleryHandler{cfg: cfg}
}

var galleryAllowedExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"webp": true, "bmp": true, "svg": true,
}

type galleryItem struct {
	Name   string    `json:"name"`
	URL    string    `json:"url"`
	Size   int64     `json:"size"`
	Mtime  time.Time `json:"mtime"`
	Preset bool      `json:"preset"`
}

func (h *GalleryHandler) Dispatch(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	if method == "GET" {
		h.List(w, r)
		return
	}
	if method == "POST" {
		action := r.URL.Query().Get("action")
		if action == "" {
			action = r.FormValue("action")
		}
		if action == "delete" {
			h.Delete(w, r)
			return
		}
		Fail(w, "未知操作")
		return
	}
	Fail(w, "不支持的请求方法")
}

func galleryScan(dir, webPrefix string, preset bool) []galleryItem {
	var out []galleryItem
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		ext = strings.TrimPrefix(ext, ".")
		if !galleryAllowedExt[ext] {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		out = append(out, galleryItem{
			Name:   name,
			URL:    webPrefix + name,
			Size:   info.Size(),
			Mtime:  info.ModTime(),
			Preset: preset,
		})
	}
	return out
}

func (h *GalleryHandler) List(w http.ResponseWriter, r *http.Request) {
	userDir := filepath.Join(h.cfg.UploadDir, "wallpapers")
	presetDir := filepath.Join(h.cfg.UploadDir, "weather")
	userWebBase := "/frontend/uploads/wallpapers/"
	presetWebBase := "/frontend/uploads/weather/"

	items := galleryScan(userDir, userWebBase, false)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Mtime.After(items[j].Mtime)
	})
	presets := galleryScan(presetDir, presetWebBase, true)
	sort.SliceStable(presets, func(i, j int) bool {
		return strings.ToLower(presets[i].Name) < strings.ToLower(presets[j].Name)
	})
	items = append(items, presets...)
	if items == nil {
		items = []galleryItem{}
	}
	OK(w, items)
}

func (h *GalleryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := BindJSON(r, &body); err != nil {
		Fail(w, "参数错误")
		return
	}
	if body.URL == "" {
		Fail(w, "缺少壁纸地址")
		return
	}

	const presetPrefix = "/frontend/uploads/weather/"
	const uploadPrefix = "/frontend/uploads/wallpapers/"
	sub, prefix, preset := "wallpapers", uploadPrefix, false
	if strings.HasPrefix(body.URL, presetPrefix) {
		sub, prefix, preset = "weather", presetPrefix, true
	} else if !strings.HasPrefix(body.URL, uploadPrefix) {
		Fail(w, "非法的壁纸地址")
		return
	}
	name := strings.TrimPrefix(body.URL, prefix)
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		Fail(w, "非法的壁纸地址")
		return
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if !galleryAllowedExt[ext] {
		Fail(w, "非法的壁纸地址")
		return
	}
	target := filepath.Join(h.cfg.UploadDir, sub, name)
	absTarget, _ := filepath.Abs(target)
	absDir, _ := filepath.Abs(filepath.Join(h.cfg.UploadDir, sub))
	if !strings.HasPrefix(absTarget, absDir) {
		Fail(w, "非法路径")
		return
	}
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			Fail(w, "壁纸不存在或已删除")
			return
		}
		Err(w, http.StatusInternalServerError, 1, "检查文件失败")
		return
	}
	if err := os.Remove(target); err != nil {
		Err(w, http.StatusInternalServerError, 1, "删除失败，请检查目录权限")
		return
	}
	if preset {
		// 记录被删除的内置壁纸，避免下次启动时由 seed 重新释放回数据目录
		markRemovedPreset(h.cfg.UploadDir, name)
	}
	OK(w, map[string]string{"deleted": name})
}

// removedPresetFile 记录用户主动删除的内置壁纸（每行一个文件名）
const removedPresetFile = ".removed"

// LoadRemovedPresets 读取已删除的内置壁纸名单
func LoadRemovedPresets(uploadDir string) map[string]bool {
	out := make(map[string]bool)
	data, err := os.ReadFile(filepath.Join(uploadDir, "weather", removedPresetFile))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			out[name] = true
		}
	}
	return out
}

func markRemovedPreset(uploadDir, name string) {
	if LoadRemovedPresets(uploadDir)[name] {
		return
	}
	f, err := os.OpenFile(filepath.Join(uploadDir, "weather", removedPresetFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(name + "\n")
}
