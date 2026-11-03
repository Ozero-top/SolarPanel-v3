package handler

import (
	"net/http"
	"strings"
	"sync"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
)

// 批量补图：给还没有图标的卡片逐个抓取站点图标。
// 约束：全局并发 4、可取消、失败只计数不重试，避免再次出现“部署即打满出站”的问题。
const iconBatchWorkers = 4

type iconBatchState struct {
	mu      sync.Mutex
	running bool
	cancel  chan struct{}
	closeMu sync.Once

	total, ok, failed int
}

var iconBatch = &iconBatchState{}

// FaviconBatchStart 启动批量补图任务（admin/editor）
func (h *ItemsHandler) FaviconBatchStart(w http.ResponseWriter, r *http.Request) {
	if s := auth.GetSessionFromRequest(r); s == nil || (s.Role != "admin" && s.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	if h.cfg == nil {
		Fail(w, "handler 未初始化")
		return
	}

	iconBatch.mu.Lock()
	if iconBatch.running {
		iconBatch.mu.Unlock()
		Fail(w, "已有补图任务在运行")
		return
	}
	iconBatch.running = true
	iconBatch.total, iconBatch.ok, iconBatch.failed = 0, 0, 0
	iconBatch.cancel = make(chan struct{})
	iconBatch.closeMu = sync.Once{}
	cancel := iconBatch.cancel
	iconBatch.mu.Unlock()

	go runIconBatch(h.cfg, cancel)
	OK(w, map[string]any{"started": true})
}

// FaviconBatchStatus 查询进度
func (h *ItemsHandler) FaviconBatchStatus(w http.ResponseWriter, r *http.Request) {
	iconBatch.mu.Lock()
	defer iconBatch.mu.Unlock()
	OK(w, map[string]any{
		"running": iconBatch.running,
		"total":   iconBatch.total,
		"ok":      iconBatch.ok,
		"failed":  iconBatch.failed,
	})
}

// FaviconBatchCancel 取消任务（正在执行的请求会跑完当前这一张）
func (h *ItemsHandler) FaviconBatchCancel(w http.ResponseWriter, r *http.Request) {
	if s := auth.GetSessionFromRequest(r); s == nil || (s.Role != "admin" && s.Role != "editor") {
		Err(w, http.StatusForbidden, 1, "无操作权限")
		return
	}
	iconBatch.mu.Lock()
	if !iconBatch.running || iconBatch.cancel == nil {
		iconBatch.mu.Unlock()
		Fail(w, "当前没有运行中的任务")
		return
	}
	cancel := iconBatch.cancel
	iconBatch.mu.Unlock()

	iconBatch.closeMu.Do(func() { close(cancel) })
	OK(w, map[string]any{"cancelled": true})
}

type iconBatchItem struct {
	id      int
	pageURL string
}

func runIconBatch(cfg *config.Config, cancel <-chan struct{}) {
	defer func() {
		iconBatch.mu.Lock()
		iconBatch.running = false
		iconBatch.mu.Unlock()
	}()

	rows, err := db.DB.Query(`SELECT id, url, lan_url FROM items WHERE icon_type <> 'image' OR icon_value = '' ORDER BY id ASC`)
	if err != nil {
		return
	}
	var list []iconBatchItem
	for rows.Next() {
		var id int
		var pageURL, lanURL string
		if err := rows.Scan(&id, &pageURL, &lanURL); err != nil {
			continue
		}
		page := strings.TrimSpace(pageURL)
		if page == "" {
			page = strings.TrimSpace(lanURL)
		}
		if page == "" {
			continue
		}
		list = append(list, iconBatchItem{id: id, pageURL: page})
	}
	rows.Close()

	iconBatch.mu.Lock()
	iconBatch.total = len(list)
	iconBatch.mu.Unlock()
	if len(list) == 0 {
		return
	}

	sem := make(chan struct{}, iconBatchWorkers)
	var wg sync.WaitGroup
	for _, it := range list {
		select {
		case <-cancel:
			wg.Wait()
			return
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it iconBatchItem) {
			defer wg.Done()
			defer func() { <-sem }()

			saved, err := autoPickFaviconFromURL(cfg, it.pageURL)
			if err != nil || saved == "" {
				countBatchResult(false)
				return
			}
			if _, err := db.DB.Exec(`UPDATE items SET icon_type='image', icon_value=? WHERE id=?`, saved, it.id); err != nil {
				countBatchResult(false)
				return
			}
			countBatchResult(true)
		}(it)
	}
	wg.Wait()
}

func countBatchResult(success bool) {
	iconBatch.mu.Lock()
	defer iconBatch.mu.Unlock()
	if success {
		iconBatch.ok++
	} else {
		iconBatch.failed++
	}
}
