package config

import (
	"os"
	"path/filepath"
)

// Config 应用配置（全部环境变量驱动，无外部配置文件）
type Config struct {
	ServerPort        string
	HTTPSPort         string
	CertFile          string
	KeyFile           string
	DBPath            string
	UploadDir         string
	SessionTTL        int
	SessionCookieName string
	CSRFCookieName    string
}

// Load 默认配置
func Load() *Config {
	wd, _ := os.Getwd()
	dataDir := filepath.Join(wd, "data")
	uploadDir := filepath.Join(dataDir, "uploads")
	dbPath := filepath.Join(dataDir, "solarpanel.db")
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(uploadDir, 0755)

	return &Config{
		ServerPort:        getEnv("SERVER_PORT", "10080"),
		HTTPSPort:         os.Getenv("HTTPS_PORT"),
		CertFile:          getEnv("CERT_FILE", filepath.Join(dataDir, "cert.pem")),
		KeyFile:           getEnv("KEY_FILE", filepath.Join(dataDir, "key.pem")),
		DBPath:            dbPath,
		UploadDir:         uploadDir,
		SessionTTL:        86400,
		SessionCookieName: "sp_session",
		CSRFCookieName:    "csrf_token",
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
