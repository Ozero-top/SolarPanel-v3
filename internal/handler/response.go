package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Err 标准错误响应
func Err(w http.ResponseWriter, httpCode int, appCode int, msg string) {
	writeRespErr(w, httpCode, appCode, msg)
}

func OK(w http.ResponseWriter, data any) {
	writeResp(w, http.StatusOK, data, "")
}

func Fail(w http.ResponseWriter, msg string) {
	writeRespErr(w, http.StatusOK, 1, msg)
}

func writeResp(w http.ResponseWriter, httpCode int, data any, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data, "msg": msg})
}

func writeRespErr(w http.ResponseWriter, httpCode int, appCode int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(map[string]any{"code": appCode, "data": nil, "msg": msg})
}

// BindJSON 从请求体解析 JSON（最多 1MB）
func BindJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return fmt.Errorf("empty body")
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("empty body")
	}
	return json.Unmarshal(body, v)
}

// GetQuery 取 query 参数
func GetQuery(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return def
}
