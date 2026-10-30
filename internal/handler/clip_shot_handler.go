package handler

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"solarpanel/internal/auth"
	"solarpanel/internal/db"
	"solarpanel/internal/urlutil"
)

// 整页截图上传上限（扩展侧会先压缩，正常在 2MB 内）
const clipShotMaxBytes = 15 * 1024 * 1024

// Screenshot POST /api/v1/clips/{id}/screenshot
// 接收扩展上传的整页截图原始字节（image/png|jpeg|webp），落盘后记录到快照表。
func (h *ClipHandler) Screenshot(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !clipEnabled() {
		Err(w, http.StatusForbidden, 1, "站点已关闭剪藏功能")
		return
	}
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		Fail(w, "参数错误")
		return
	}
	it, err := loadItem(id)
	if err != nil {
		Err(w, http.StatusNotFound, 1, "剪藏不存在")
		return
	}
	if p.Role != "admin" && it.UserID != int(p.UID) {
		Err(w, http.StatusForbidden, 1, "只能为自己剪藏的卡片上传截图")
		return
	}

	if r.ContentLength > clipShotMaxBytes {
		Err(w, http.StatusRequestEntityTooLarge, 1, "截图超过 15MB")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, clipShotMaxBytes+1))
	if err != nil {
		Fail(w, "读取截图失败")
		return
	}
	if len(data) == 0 {
		Fail(w, "截图为空")
		return
	}
	if len(data) > clipShotMaxBytes {
		Err(w, http.StatusRequestEntityTooLarge, 1, "截图超过 15MB")
		return
	}
	ext := shotImageExt(data, r.Header.Get("Content-Type"))
	if ext == "" {
		Fail(w, "仅支持 PNG/JPEG/WebP 图片")
		return
	}

	dir := filepath.Join(h.cfg.UploadDir, "clips", "shots")
	if err := os.MkdirAll(dir, 0755); err != nil {
		Err(w, http.StatusInternalServerError, 1, "保存截图失败")
		return
	}
	sum := md5.Sum(data)
	name := fmt.Sprintf("%d-%s%s", id, hex.EncodeToString(sum[:])[:10], ext)
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		Err(w, http.StatusInternalServerError, 1, "保存截图失败")
		return
	}
	webPath := "/frontend/uploads/clips/shots/" + name

	// 截图元数据（模式 / 宽高），由扩展通过 X-Shot-* 头携带；缺失时留空
	meta := clipShotMeta{
		Mode:   strings.ToLower(strings.TrimSpace(r.Header.Get("X-Shot-Mode"))),
		Width:  atoiHeader(r.Header.Get("X-Shot-Width")),
		Height: atoiHeader(r.Header.Get("X-Shot-Height")),
		Bytes:  len(data),
	}
	if meta.Mode != "full" && meta.Mode != "viewport" {
		meta.Mode = ""
	}
	metaJSON := ""
	if b, err := json.Marshal(meta); err == nil && (meta.Mode != "" || meta.Width > 0 || meta.Height > 0) {
		metaJSON = string(b)
	}

	// 已有快照则只更新截图字段；没有快照时为卡片补一条最小记录，方便阅读页展示
	var exists int
	db.DB.QueryRow(`SELECT COUNT(*) FROM clip_contents WHERE item_id = ?`, id).Scan(&exists)
	if exists > 0 {
		var old string
		db.DB.QueryRow(`SELECT screenshot_path FROM clip_contents WHERE item_id = ?`, id).Scan(&old)
		if _, err := db.DB.Exec(`UPDATE clip_contents SET screenshot_path = ?, screenshot_meta = ? WHERE item_id = ?`,
			webPath, metaJSON, id); err != nil {
			os.Remove(filepath.Join(dir, name))
			Err(w, http.StatusInternalServerError, 1, "保存截图失败")
			return
		}
		if old != "" && old != webPath {
			removeClipFile(h.cfg, old)
		}
	} else {
		_, err = db.DB.Exec(`INSERT INTO clip_contents
			(item_id, url_key, url_original, source, excerpt, content_md, cover_path,
			 fetched_at, http_status, content_hash, word_count, fetch_error, screenshot_path, screenshot_meta)
			VALUES (?,?,?,'screenshot','','','',?,0,'',0,'',?,?)`,
			id, urlutil.Key(it.URL), it.URL, db.NowStr(), webPath, metaJSON)
		if err != nil {
			os.Remove(filepath.Join(dir, name))
			Err(w, http.StatusInternalServerError, 1, "保存截图失败")
			return
		}
	}

	auth.AuditLog(w, r, "clip.screenshot", fmt.Sprintf("item:%d", id), "success", clipActor(p), p.Role,
		fmt.Sprintf("bytes=%d mode=%s", len(data), meta.Mode))
	OK(w, map[string]any{
		"screenshot_path": webPath,
		"screenshot_meta": metaJSON,
		"bytes":           len(data),
		"mode":            meta.Mode,
		"width":           meta.Width,
		"height":          meta.Height,
	})
}

// clipShotMeta 整页截图元数据：full=整页拼接，viewport=仅可视区
type clipShotMeta struct {
	Mode   string `json:"mode"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Bytes  int    `json:"bytes"`
}

// atoiHeader 读取数值型请求头，非法值返回 0
func atoiHeader(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// shotImageExt 通过文件头判断图片类型，避免把任意内容当图片保存
func shotImageExt(data []byte, contentType string) string {
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return ".png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return ".jpg"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp"
	}
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "webp"):
		return ".webp"
	}
	return ""
}
