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
	BackupDir         string
	SessionTTL        int
	SessionCookieName string
	CSRFCookieName    string
	// NAS 监控：宿主机根目录（容器内一般为 /host，本机运行留空 = /）
	NASHostRoot string
	// NAS 监控：Docker 守护进程 socket（各 NAS 均为 /var/run/docker.sock）
	DockerSocket string
}

// Load 默认配置
func Load() *Config {
	wd, _ := os.Getwd()
	dataDir := filepath.Join(wd, "data")
	uploadDir := filepath.Join(dataDir, "uploads")
	dbPath := filepath.Join(dataDir, "solarpanel.db")
	backupDir := filepath.Join(dataDir, "backups")
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(uploadDir, 0755)
	os.MkdirAll(backupDir, 0755)

	hostRoot := getEnv("NAS_HOST_ROOT", "/host")
	if _, err := os.Stat(hostRoot); err != nil {
		hostRoot = "" // 未挂载宿主机根目录时退回容器自身视角
	}

	return &Config{
		ServerPort:        getEnv("SERVER_PORT", "10086"),
		HTTPSPort:         os.Getenv("HTTPS_PORT"),
		CertFile:          getEnv("CERT_FILE", filepath.Join(dataDir, "cert.pem")),
		KeyFile:           getEnv("KEY_FILE", filepath.Join(dataDir, "key.pem")),
		DBPath:            dbPath,
		UploadDir:         uploadDir,
		BackupDir:         backupDir,
		SessionTTL:        86400,
		SessionCookieName: "sp_session",
		CSRFCookieName:    "csrf_token",
		NASHostRoot:       hostRoot,
		DockerSocket:      getEnv("DOCKER_SOCKET", "/var/run/docker.sock"),
	}
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
