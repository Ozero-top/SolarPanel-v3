package auth

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"solarpanel/internal/db"
)

// RateLimiter 基于 SQLite 表的 IP+端点 滑动窗口限流
type RateLimiter struct {
	mu sync.Mutex
}

// NewRateLimiter 构造；后台每 30 秒清理过期窗口
func NewRateLimiter() *RateLimiter {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			if db.DB != nil {
				cutoff := time.Now().Add(-5 * time.Minute).Format("2006-01-02 15:04:05")
				db.DB.Exec("DELETE FROM rate_limits WHERE window_start < ?", cutoff)
			}
		}
	}()
	return &RateLimiter{}
}

// Guard 执行一次检查+计数；超限返回 false 并写 429 响应
func (rl *RateLimiter) Guard(w http.ResponseWriter, r *http.Request, endpoint string, maxRequests, windowSec int) bool {
	if db.DB == nil {
		return true
	}

	ip := ClientIP(r)
	now := time.Now()
	nowStr := now.Format("2006-01-02 15:04:05")

	rl.mu.Lock()
	defer rl.mu.Unlock()

	var count int
	var windowStr string
	err := db.DB.QueryRow("SELECT count, window_start FROM rate_limits WHERE ip = ? AND endpoint = ?", ip, endpoint).Scan(&count, &windowStr)

	if err != nil {
		// 无记录 → 创建
		db.DB.Exec("INSERT INTO rate_limits (ip, endpoint, count, window_start) VALUES (?, ?, 1, ?)", ip, endpoint, nowStr)
		return true
	}

	// 检查是否跨越窗口
	var windowStart time.Time
	if ws, err := time.Parse("2006-01-02 15:04:05", windowStr); err == nil {
		windowStart = ws
	}
	if now.Sub(windowStart) > time.Duration(windowSec)*time.Second {
		db.DB.Exec("UPDATE rate_limits SET count = 1, window_start = ? WHERE ip = ? AND endpoint = ?", nowStr, ip, endpoint)
		return true
	}

	if count >= maxRequests {
		retryAfter := int(time.Duration(windowSec)*time.Second - now.Sub(windowStart))
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code":429,"msg":"请求过于频繁，请稍后再试"}`))
		return false
	}

	db.DB.Exec("UPDATE rate_limits SET count = count + 1 WHERE ip = ? AND endpoint = ?", ip, endpoint)
	return true
}
