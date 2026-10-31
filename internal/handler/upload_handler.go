package handler

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gen2brain/webp"

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

// convertWallpaperToWebP 将静态壁纸（jpg/png/gif 首帧）转为 WebP，失败返回 nil 保留原图
func convertWallpaperToWebP(data []byte) []byte {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if err := webp.Encode(&buf, img, webp.Options{Quality: 82, Method: 4}); err != nil {
		return nil
	}
	return buf.Bytes()
}

var webpConvertibleExt = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
}

func (h *UploadHandler) Upload(w http.ResponseWriter, r *http.Request) {
	sess := auth.GetSessionFromRequest(r)
	if sess == nil || (sess.Role != "admin" && sess.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	// 必须先限制请求体再解析表单，否则 FormValue 会先把整个 multipart 读进内存
	const maxUploadBytes = 10 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1<<20)

	typ := strings.ToLower(strings.TrimSpace(r.FormValue("type")))
	if typ == "" {
		typ = "icon"
	}

	limit, ok := typeLimits[typ]
	if !ok {
		Fail(w, "type 参数无效")
		return
	}

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

	// 先处理内容（壁纸转 WebP 会改变扩展名），再确定落盘文件名
	var content io.Reader = file
	if ext == "svg" {
		// SVG 可携带脚本，落盘前统一清洗（与 favicon / 备份导入保持一致）
		raw, err := io.ReadAll(io.LimitReader(file, limit))
		if err != nil {
			Err(w, http.StatusInternalServerError, 1, "读取文件失败")
			return
		}
		content = strings.NewReader(string(sanitizeSVG(raw)))
	} else if typ == "wallpaper" && webpConvertibleExt[ext] {
		raw, err := io.ReadAll(io.LimitReader(file, limit))
		if err != nil {
			Err(w, http.StatusInternalServerError, 1, "读取文件失败")
			return
		}
		if converted := convertWallpaperToWebP(raw); converted != nil {
			ext = "webp"
			content = bytes.NewReader(converted)
		} else {
			// 解码失败（如动画 WebP 误判）时保留原图，不阻断上传
			content = bytes.NewReader(raw)
		}
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

	if _, err := io.Copy(dst, content); err != nil {
		Err(w, http.StatusInternalServerError, 1, "写入文件失败")
		return
	}

	url := fmt.Sprintf("/frontend/uploads/%s/%s", subdir, filename)
	OK(w, map[string]string{"url": url})
}
