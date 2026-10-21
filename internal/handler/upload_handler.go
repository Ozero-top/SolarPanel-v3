package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
)

type UploadHandler struct {
	cfg *config.Config
}

func NewUploadHandler(cfg *config.Config) *UploadHandler {
	return &UploadHandler{cfg: cfg}
}

var allowedExts = map[string]bool{
	"jpg":  true, "jpeg": true, "png": true, "gif": true,
	"webp": true, "ico": true, "bmp": true, "svg": true,
}

var typeLimits = map[string]int64{
	"wallpaper": 10 * 1024 * 1024,
	"icon":      5 * 1024 * 1024,
	"logo":      5 * 1024 * 1024,
}

var typeSubdirs = map[string]string{
	"icon":      "icons",
	"logo":      "logos",
	"wallpaper": "wallpapers",
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *UploadHandler) Upload(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	typ := strings.ToLower(strings.TrimSpace(r.FormValue("type")))
	if typ == "" {
		typ = "icon"
	}

	limit, ok := typeLimits[typ]
	if !ok {
		Fail(w, "type 参数无效")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, limit+1024)

	file, header, err := r.FormFile("file")
	if err != nil {
		Fail(w, "未找到上传文件: "+err.Error())
		return
	}
	defer file.Close()

	if header.Size > limit {
		Fail(w, fmt.Sprintf("文件过大（最大 %dMB）", limit/1024/1024))
		return
	}

	origName := header.Filename
	ext := strings.ToLower(filepath.Ext(origName))
	ext = strings.TrimPrefix(ext, ".")
	if !allowedExts[ext] {
		Fail(w, "不支持的文件扩展名: "+ext)
		return
	}

	subdir, ok := typeSubdirs[typ]
	if !ok {
		subdir = "others"
	}

	targetDir := filepath.Join(h.cfg.UploadDir, subdir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		Err(w, http.StatusInternalServerError, 1, "创建目录失败")
		return
	}

	ts := time.Now().Format("20060102150405")
	filename := fmt.Sprintf("%s_%s.%s", ts, randomHex(4), ext)
	targetPath := filepath.Join(targetDir, filename)

	dst, err := os.Create(targetPath)
	if err != nil {
		Err(w, http.StatusInternalServerError, 1, "保存文件失败")
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		Err(w, http.StatusInternalServerError, 1, "写入文件失败")
		return
	}

	url := fmt.Sprintf("/frontend/uploads/%s/%s", subdir, filename)
	OK(w, map[string]string{"url": url})
}
