package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"solarpanel/internal/db"
)

// TokenPrincipal Bearer Token 校验通过后的身份
type TokenPrincipal struct {
	TokenID  int
	UID      uint
	Username string
	Name     string
	Role     string
	Scopes   map[string]bool
}

// HasScope 判断是否具备指定 scope
func (p *TokenPrincipal) HasScope(scope string) bool {
	if p == nil {
		return false
	}
	return p.Scopes[scope]
}

// GenerateToken 生成新 Token：明文只返回一次，数据库只存 SHA-256
func GenerateToken() (plain, hash, prefix string, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", "", "", err
	}
	plain = "sp_" + base64.RawURLEncoding.EncodeToString(b[:])
	hash = HashToken(plain)
	prefix = plain
	if len(prefix) > 10 {
		prefix = prefix[:10] + "…"
	}
	return plain, hash, prefix, nil
}

// HashToken 计算 Token 的 SHA-256 十六进制摘要
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// ParseScopes 把逗号分隔的 scope 字符串转为集合
func ParseScopes(raw string) map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			m[s] = true
		}
	}
	return m
}

// JoinScopes 把 scope 集合按固定顺序拼为字符串
func JoinScopes(scopes map[string]bool) string {
	order := []string{"clip:write", "clip:read", "groups:read"}
	out := make([]string, 0, len(scopes))
	for _, s := range order {
		if scopes[s] {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

// lastUsedThrottle 避免每次请求都写 last_used_at
var lastUsedThrottle sync.Map // tokenID(int) -> time.Time

// AuthenticateBearer 校验 Authorization: Bearer，失败统一返回同一错误，避免枚举
func AuthenticateBearer(r *http.Request) (*TokenPrincipal, error) {
	if r == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" || !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return nil, fmt.Errorf("unauthorized")
	}
	plain := strings.TrimSpace(raw[7:])
	if !strings.HasPrefix(plain, "sp_") || len(plain) < 20 {
		return nil, fmt.Errorf("unauthorized")
	}
	hash := HashToken(plain)

	var (
		tokenID, userID, status           int
		username, name, role, scopes      string
		expiresAt, revokedAt, storedHash  string
	)
	err := db.DB.QueryRow(`SELECT t.id, t.user_id, u.username, u.name, u.status, u.role,
			t.scopes, t.expires_at, t.revoked_at, t.token_hash
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`, hash).
		Scan(&tokenID, &userID, &username, &name, &status, &role, &scopes, &expiresAt, &revokedAt, &storedHash)
	if err != nil {
		return nil, fmt.Errorf("unauthorized")
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hash)) != 1 {
		return nil, fmt.Errorf("unauthorized")
	}
	if revokedAt != "" || status != 1 {
		return nil, fmt.Errorf("unauthorized")
	}
	if expiresAt != "" {
		if t, e := parseTokenTime(expiresAt); e == nil && time.Now().After(t) {
			return nil, fmt.Errorf("unauthorized")
		}
	}
	if role != "admin" && role != "editor" {
		return nil, fmt.Errorf("forbidden")
	}

	p := &TokenPrincipal{
		TokenID:  tokenID,
		UID:      uint(userID),
		Username: username,
		Name:     name,
		Role:     role,
		Scopes:   ParseScopes(scopes),
	}
	touchTokenUsage(tokenID)
	return p, nil
}

// parseTokenTime 兼容 SQLite datetime 与 RFC3339 两种格式
func parseTokenTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// touchTokenUsage 5 分钟内不重复写库
func touchTokenUsage(tokenID int) {
	if v, ok := lastUsedThrottle.Load(tokenID); ok {
		if time.Since(v.(time.Time)) < 5*time.Minute {
			return
		}
	}
	lastUsedThrottle.Store(tokenID, time.Now())
	if db.DB != nil {
		db.DB.Exec("UPDATE api_tokens SET last_used_at = ? WHERE id = ?", db.NowStr(), tokenID)
	}
}
