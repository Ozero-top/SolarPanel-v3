package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Session 内存会话
type Session struct {
	UID       uint
	CSRFToken string
	Role      string
	Expires   time.Time
}

type Store struct {
	TTL      time.Duration
	mu       sync.RWMutex
	sessions map[string]*Session
	csrfMap  map[string]string
}

func NewSessionStore(ttlSeconds int) *Store {
	s := &Store{
		TTL:      time.Duration(ttlSeconds) * time.Second,
		sessions: make(map[string]*Session),
		csrfMap:  make(map[string]string),
	}
	go s.gcLoop()
	return s
}

func (s *Store) gcLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		s.gc()
	}
}

func (s *Store) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for sid, sess := range s.sessions {
		if now.After(sess.Expires) {
			delete(s.csrfMap, sess.CSRFToken)
			delete(s.sessions, sid)
		}
	}
}

func NewSID() string {
	var b [32]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func NewCSRFToken() string {
	var b [32]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Store) CreateSession(uid uint, role string) (sid, csrf string) {
	sid = NewSID()
	csrf = NewCSRFToken()
	s.mu.Lock()
	s.sessions[sid] = &Session{UID: uid, CSRFToken: csrf, Role: role, Expires: time.Now().Add(s.TTL)}
	s.csrfMap[csrf] = sid
	s.mu.Unlock()
	return
}

func (s *Store) GetSession(sid string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sid]
	if !ok || time.Now().After(sess.Expires) {
		return nil
	}
	return sess
}

func (s *Store) DestroySession(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sid]; ok {
		delete(s.csrfMap, sess.CSRFToken)
		delete(s.sessions, sid)
	}
}

func (s *Store) RotateCSRF(sid string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sid]
	if !ok {
		return ""
	}
	delete(s.csrfMap, sess.CSRFToken)
	sess.CSRFToken = NewCSRFToken()
	s.csrfMap[sess.CSRFToken] = sid
	sess.Expires = time.Now().Add(s.TTL)
	return sess.CSRFToken
}

func (s *Store) Touch(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sid]; ok {
		sess.Expires = time.Now().Add(s.TTL)
	}
}

// sessionCtxKey 上下文键类型（防止冲突）
type ctxKey string

const (
	ctxSession   ctxKey = "session"
	ctxSessionID ctxKey = "session_id"
)

// GetSessionFromRequest 从请求上下文取 session（handler 内调用）
func GetSessionFromRequest(r *http.Request) *Session {
	sess, _ := r.Context().Value(ctxSession).(*Session)
	return sess
}

func getSessionIDFromRequest(r *http.Request) string {
	sid, _ := r.Context().Value(ctxSessionID).(string)
	return sid
}

// GetSessionIDFromRequest 从请求上下文取 session ID（handler 内调用）
func GetSessionIDFromRequest(r *http.Request) string {
	sid, _ := r.Context().Value(ctxSessionID).(string)
	return sid
}

// SessionMiddleware 解析 session cookie，注入 context
func (s *Store) SessionMiddleware(cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sid, _ := r.Cookie(cookieName)
			if sid != nil {
				if sess := s.GetSession(sid.Value); sess != nil {
					ctx := r.Context()
					ctx = context.WithValue(ctx, ctxSession, sess)
					ctx = context.WithValue(ctx, ctxSessionID, sid.Value)
					if r.Method != "GET" {
						s.Touch(sid.Value)
					}
					r = r.WithContext(ctx)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CSRFMiddleware 写请求校验 X-CSRF-Token
func (s *Store) CSRFMiddleware(csrfCookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			method := strings.ToUpper(r.Method)
			if method != "GET" && method != "HEAD" && method != "OPTIONS" {
				headerToken := r.Header.Get("X-CSRF-Token")
				sess := GetSessionFromRequest(r)
				if sess == nil || headerToken == "" || headerToken != sess.CSRFToken {
					http.Error(w, `{"code":1,"data":null,"msg":"CSRF 令牌无效或已过期，请刷新页面后重试"}`, http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireLogin 必须登录
func RequireLogin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if GetSessionFromRequest(r) == nil {
				http.Error(w, `{"code":1,"data":null,"msg":"未登录或登录已过期"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRoles 必须指定角色之一
func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := GetSessionFromRequest(r)
			if sess == nil {
				http.Error(w, `{"code":1,"data":null,"msg":"未登录或登录已过期"}`, http.StatusUnauthorized)
				return
			}
			for _, role := range roles {
				if sess.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, `{"code":1,"data":null,"msg":"当前账号权限不足"}`, http.StatusForbidden)
		})
	}
}

// SetCookies 下发 session + csrf cookie
func SetCookies(w http.ResponseWriter, sid, sessCSRF, sessionCookieName, csrfCookieName string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 86400,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: sessCSRF, Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode, MaxAge: 86400,
	})
}

// ClearCookies 登出时清除
func ClearCookies(w http.ResponseWriter, sessionCookieName, csrfCookieName string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: "", Path: "/", MaxAge: -1})
}
