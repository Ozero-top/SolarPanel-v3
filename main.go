package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/pem"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"solarpanel/internal/auth"
	"solarpanel/internal/config"
	"solarpanel/internal/db"
	"solarpanel/internal/handler"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed all:frontend
var frontendFS embed.FS

// uploadCacheExt 长缓存（7 天）的图片扩展名
var uploadCacheExt = map[string]bool{
	".webp": true, ".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".ico": true, ".bmp": true, ".svg": true,
}

func contentTypeByExt(path string) string {
	switch filepath.Ext(path) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".ico":
		return "image/x-icon"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func seedPresetWallpapers(uploadDir string) {
	presetDir := filepath.Join(uploadDir, "weather")
	if err := os.MkdirAll(presetDir, 0755); err != nil {
		log.Printf("[seed] 创建预置壁纸目录失败: %v", err)
		return
	}

	entries, err := fs.ReadDir(frontendFS, "frontend/uploads/weather")
	if err != nil {
		return
	}
	removed := handler.LoadRemovedPresets(uploadDir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if removed[e.Name()] {
			continue
		}
		dst := filepath.Join(presetDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		srcData, err := frontendFS.ReadFile("frontend/uploads/weather/" + e.Name())
		if err != nil {
			continue
		}
		if err := os.WriteFile(dst, srcData, 0644); err != nil {
			log.Printf("[seed] 写入 preset 壁纸 %s 失败: %v", e.Name(), err)
			continue
		}
		log.Printf("[seed] 预置壁纸 %s 已复制", e.Name())
	}
}

func extractVersion() string {
	data, err := frontendFS.ReadFile("frontend/assets/js/api.js")
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`const\s+APP_VERSION\s*=\s*['"]([^'"]+)['"]`)
	m := re.FindSubmatch(data)
	if len(m) < 2 {
		return ""
	}
	return string(m[1])
}

func generateSelfSignedCert(certPath, keyPath string) error {
	if _, err := os.Stat(certPath); err == nil {
		if _, err2 := os.Stat(keyPath); err2 == nil {
			return nil
		}
	}
	log.Println("[tls] 未找到 TLS 证书，正在生成自签名证书...")

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("生成私钥失败: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return fmt.Errorf("生成序列号失败: %w", err)
	}

	ips := []net.IP{}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				ips = append(ips, ipNet.IP)
			}
		}
	}
	ips = append(ips, net.IPv4(127, 0, 0, 1), net.IPv6loopback)

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"SolarPanel Self-Signed"},
			CommonName:   "SolarPanel",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
		DNSNames:              []string{"localhost", "solarpanel.local"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("创建证书失败: %w", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("写证书文件失败: %w", err)
	}
	defer certOut.Close()
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("写私钥文件失败: %w", err)
	}
	defer keyOut.Close()
	privBytes, _ := x509.MarshalECPrivateKey(priv)
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	log.Println("[tls] 自签名证书已生成:", certPath, keyPath)
	return nil
}

func main() {
	cfg := config.Load()

	if err := db.Init(cfg); err != nil {
		log.Fatal("[db] 初始化失败:", err)
	}
	defer db.Close()
	// 剪藏查重键兜底回填（迁移已跑过；这里覆盖旧库/导入数据）
	db.BackfillURLKeys()
	// 全文索引与快照表条数不一致时重建（迁移失败或手工改库后自愈）
	db.EnsureClipFTS()

	seedPresetWallpapers(cfg.UploadDir)
	sessionStore := auth.NewSessionStore(cfg.SessionTTL)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// 不启用 middleware.RealIP：它会无条件信任 X-Forwarded-For，
	// 客户端可伪造 IP 绕过限流；由 auth.ClientIP 只在前置代理为内网时采信转发头
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			next.ServeHTTP(w, r)
		})
	})
	// session 注入
	r.Use(sessionStore.SessionMiddleware(cfg.SessionCookieName))

	rateLimiter := auth.NewRateLimiter()

	// ===== handler 实例化 =====
	currentVersion := extractVersion()
	handler.AppVersion = currentVersion

	authHandler := handler.NewAuthHandler(sessionStore, cfg)
	installHandler := handler.InstallHandler{}
	uploadHandler := handler.NewUploadHandler(cfg)
	galleryHandler := handler.NewGalleryHandler(cfg)
	weatherHandler := handler.NewWeatherHandler(cfg)
	publicHandler := &handler.PublicHandler{}
	newsHandler := &handler.NewsHandler{}
	settingsHandler := &handler.SettingsHandler{}
	groupsHandler := &handler.GroupsHandler{}
	itemsHandler := handler.NewItemsHandler(cfg)
	faviconProxyHandler := handler.NewFaviconProxyHandler(cfg)
	backupHandler := handler.NewBackupHandler(cfg)
	userHandler := &handler.UserHandler{}
	auditHandler := handler.NewAuditHandler()
	feedsHandler := handler.NewFeedsHandler()
	importHandler := handler.NewImportHandler()
	wallpaperHandler := handler.NewWallpaperHandler(cfg)
	nasHandler := handler.NewNASHandler(cfg)
	versionHandler := handler.VersionHandler{}
	clipHandler := handler.NewClipHandler(cfg)
	clipTokenHandler := handler.ClipTokenHandler{}
	clipShareHandler := handler.NewClipShareHandler(clipHandler)

	// ===== 限流中间件（chi 适配器）=====
	rateLimitMW := func(endpoint string, max, windowSec int) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !rateLimiter.Guard(w, r, endpoint, max, windowSec) {
					return
				}
				next.ServeHTTP(w, r)
			})
		}
	}

	// ===== 公共路由 + 登录后路由（合并成一个 Route，chi 不支持多次同路径 Route） =====
	r.Route("/api", func(api chi.Router) {
		// 公共（无需登录）
		api.With(rateLimitMW("public", 60, 60)).Get("/public.php", publicHandler.Public)
		api.With(rateLimitMW("favicon", 120, 60)).Get("/favicon.php", faviconProxyHandler.Proxy)
		api.Get("/install.php", installHandler.Status)
		api.Post("/install.php", installHandler.Setup)
		api.With(rateLimitMW("weather", 30, 60)).Get("/weather.php", weatherHandler.Get)
		api.With(rateLimitMW("news", 30, 60)).Post("/news.php", newsHandler.Dispatch)
		api.With(rateLimitMW("news", 30, 60)).Get("/news.php", newsHandler.Dispatch)
		api.With(rateLimitMW("news", 30, 60)).Get("/news/meta", newsHandler.Sources)
		api.With(rateLimitMW("news", 30, 60)).Get("/news/all", newsHandler.All)
		api.With(rateLimitMW("news", 30, 60)).Get("/news/source", newsHandler.Source)
		api.With(rateLimitMW("wallpaper", 30, 60)).Post("/wallpaper.php", wallpaperHandler.Dispatch)
		api.With(rateLimitMW("wallpaper", 30, 60)).Get("/wallpaper.php", wallpaperHandler.Dispatch)
		// 面板轮询 + 详情弹窗 + 多标签页共享同一出口 IP，限流放宽到 240 次/分
		api.With(rateLimitMW("nas", 240, 60)).Get("/nas.php", nasHandler.Get)
		// 版本检查：结果在后端缓存，正常情况不会频繁访问外网
		api.With(rateLimitMW("version", 30, 60)).Get("/version.php", versionHandler.Get)
		// auth.php — 全局 session 已注入
		api.Post("/auth.php", authHandler.Dispatch)
		api.Get("/auth.php", authHandler.Dispatch)

		// 登录后路由组
		api.Group(func(authed chi.Router) {
			authed.Use(auth.RequireLogin())
			authed.Use(sessionStore.CSRFMiddleware(cfg.CSRFCookieName))
			authed.Post("/settings.php", settingsHandler.Dispatch)
			authed.Get("/settings.php", settingsHandler.Dispatch)
			authed.Post("/groups.php", groupsHandler.Dispatch)
			authed.Get("/groups.php", groupsHandler.Dispatch)
			authed.Post("/items.php", itemsHandler.Dispatch)
			authed.Get("/items.php", itemsHandler.Dispatch)
			authed.Post("/upload.php", uploadHandler.Upload)
			authed.Post("/gallery.php", galleryHandler.Dispatch)
			authed.Get("/gallery.php", galleryHandler.Dispatch)
			authed.Post("/backup.php", backupHandler.Dispatch)
			authed.Get("/backup.php", backupHandler.Dispatch)
			authed.Post("/user.php", userHandler.Dispatch)
			authed.Get("/user.php", userHandler.Dispatch)
			authed.Post("/audit.php", auditHandler.Dispatch)
			authed.Get("/audit.php", auditHandler.Dispatch)
			authed.Post("/feeds.php", feedsHandler.Dispatch)
			authed.Get("/feeds.php", feedsHandler.Dispatch)
			authed.Post("/import.php", importHandler.Dispatch)
		})
	})

	// ===== 剪藏 API v1（Bearer Token / 登录会话双鉴权，CORS 允许 *）=====
	clipCORS := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-CSRF-Token")
			h.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Use(clipCORS)
		v1.Use(rateLimitMW("clip", 120, 60))
		v1.Post("/clips", clipHandler.Create)
		v1.Delete("/clips", clipHandler.Delete)
		v1.Get("/clips/search", clipHandler.Search)
		v1.Get("/clips/{id}", clipHandler.Detail)
		v1.Post("/clips/{id}/refetch", clipHandler.Refetch)
		v1.Delete("/clips/{id}/content", clipHandler.DeleteContent)
		v1.Get("/me", clipHandler.Me)
		v1.Get("/groups", clipHandler.Groups)
		v1.Post("/preview", clipHandler.Preview)
		v1.Get("/tokens", clipTokenHandler.List)
		v1.Post("/tokens", clipTokenHandler.Create)
		v1.Delete("/tokens", clipTokenHandler.Revoke)
	})

	// ===== 保护 admin.html / login.html =====
	protectAdminHTML := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := auth.GetSessionFromRequest(r)
			if sess == nil || sess.Role == "guest" {
				http.Redirect(w, r, "/login.html", http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	protectLoginHTML := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := auth.GetSessionFromRequest(r)
			if sess != nil && sess.Role != "guest" {
				http.Redirect(w, r, "/admin.html", http.StatusFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	// 静态 embed 读文件 helper
	serveEmbed := func(embedPath, contentType string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			data, err := frontendFS.ReadFile(embedPath)
			if err != nil {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		}
	}

	r.Get("/", serveEmbed("frontend/index.html", "text/html; charset=utf-8"))
	r.Get("/favicon.ico", serveEmbed("frontend/favicon.ico", "image/x-icon"))
	r.With(protectLoginHTML).Get("/login.html", serveEmbed("frontend/login.html", "text/html; charset=utf-8"))
	r.With(protectAdminHTML).Get("/admin.html", serveEmbed("frontend/admin.html", "text/html; charset=utf-8"))

	// 剪藏落地页 / PWA 分享入口
	r.Get("/clip", serveEmbed("frontend/clip.html", "text/html; charset=utf-8"))
	r.Get("/clip-share", clipShareHandler.Share)
	r.Post("/clip-share", clipShareHandler.Share)
	// 正文快照阅读页（P1）：需要登录后访问
	r.With(protectAdminHTML).Get("/read/{id}", serveEmbed("frontend/clip-read.html", "text/html; charset=utf-8"))

	// 用户上传文件目录（磁盘直读）
	// 上传内容来源不可信（可能含 SVG），用 CSP sandbox + default-src 'none' 阻止脚本执行
	fileServer := http.FileServer(http.Dir(cfg.UploadDir))
	guardUploads := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox")
			// 壁纸与图标体积大且极少变动，长缓存减少重复下载
			if uploadCacheExt[strings.ToLower(filepath.Ext(r.URL.Path))] {
				w.Header().Set("Cache-Control", "public, max-age=604800")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600")
			}
			next.ServeHTTP(w, r)
		})
	}
	r.Mount("/frontend/uploads", guardUploads(http.StripPrefix("/frontend/uploads", fileServer)))
	r.Mount("/uploads", guardUploads(http.StripPrefix("/uploads", fileServer)))

	// embed 静态资源兜底（/assets/xxx 或 /frontend/assets/xxx）
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		cleanPath := strings.TrimPrefix(path, "/frontend")
		if cleanPath == "" {
			cleanPath = path
		}
		// 防止通过 /frontend/admin.html 绕过登录保护
		switch cleanPath {
		case "/admin.html":
			if sess := auth.GetSessionFromRequest(r); sess == nil || sess.Role == "guest" {
				http.Redirect(w, r, "/login.html", http.StatusFound)
				return
			}
		case "/login.html":
			if sess := auth.GetSessionFromRequest(r); sess != nil && sess.Role != "guest" {
				http.Redirect(w, r, "/admin.html", http.StatusFound)
				return
			}
		}
		embedPath := "frontend" + cleanPath
		data, err := frontendFS.ReadFile(embedPath)
		if err == nil {
			ct := contentTypeByExt(cleanPath)
			w.Header().Set("Content-Type", ct)
			w.WriteHeader(http.StatusOK)
			w.Write(data)
			return
		}
		http.Error(w, "not found: "+path, http.StatusNotFound)
	})

	// ===== 启动日志 =====
	log.Println("==============================================")
	log.Println(" SolarPanel (lightweight) 已启动")
	log.Println(" 版本:", currentVersion)
	log.Println(" 管理员默认账号: admin / admin123")
	log.Println("==============================================")

	// 审计日志自动清理：启动时先跑一次（清理历史遗留），然后每小时跑一次
	go func() {
		auth.AuditCleanup()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			auth.AuditCleanup()
		}
	}()

	// 自动备份调度：默认关闭，开启后按 1/3/5/7/9 天间隔生成，最多保留 5 份
	go backupHandler.StartAutoBackup()

	// 剪藏幂等记录清理：启动跑一次，之后每小时清理 24 小时前数据
	go func() {
		handler.CleanupClipRequests()
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			handler.CleanupClipRequests()
		}
	}()

	// HTTP 始终启动（方便反代）
	httpAddr := ":" + cfg.ServerPort
	go func() {
		srv := &http.Server{
			Addr:         httpAddr,
			Handler:      r,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
		}
		log.Printf("[http] 监听 http://localhost%s", httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("[http] listen error:", err)
		}
	}()

	// HTTPS（HTTPS_PORT 非空时启用）
	if cfg.HTTPSPort != "" {
		if err := generateSelfSignedCert(cfg.CertFile, cfg.KeyFile); err != nil {
			log.Printf("[https] 证书准备失败（跳过 HTTPS）: %v", err)
		} else {
			httpsAddr := ":" + cfg.HTTPSPort
			go func() {
				cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
				if err != nil {
					log.Fatalf("[https] 加载证书失败: %v", err)
				}
				tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
				srv := &http.Server{
					Addr:         httpsAddr,
					Handler:      r,
					TLSConfig:    tlsConfig,
					ReadTimeout:  30 * time.Second,
					WriteTimeout: 60 * time.Second,
				}
				log.Printf("[https] 监听 https://localhost%s", httpsAddr)
				if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
					log.Fatal("[https] listen error:", err)
				}
			}()
		}
	}

	select {}
}
