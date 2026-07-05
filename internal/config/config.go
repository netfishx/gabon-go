// Package config 从环境变量装载配置，缺失即 fail fast。
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const minJWTSecretLen = 32

// Config 全部运行时配置，一次装载、启动即校验。
type Config struct {
	DatabaseURL string
	JWTSecret   []byte
	HTTPAddr    string
	// AdminUsername/AdminPassword 为初始管理员引导凭据，仅在 admins 表为空时使用。
	AdminUsername string
	AdminPassword string

	// 对象存储（ADR-0005：S3 协议，endpoint 可配）
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
	// CDNBaseURL 播放地址基础域名（如 https://cdn.example.com），回源对象存储
	CDNBaseURL string

	// 转码 worker 池（ADR-0003）
	TranscodeWorkers int
	TranscodeTimeout time.Duration
}

// Load 从环境变量装载配置，必填项缺失立即报错（fail fast）。
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		JWTSecret:     []byte(os.Getenv("JWT_SECRET")),
		HTTPAddr:      os.Getenv("HTTP_ADDR"),
		AdminUsername: os.Getenv("ADMIN_USERNAME"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		S3Endpoint:    os.Getenv("S3_ENDPOINT"),
		S3AccessKey:   os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:   os.Getenv("S3_SECRET_KEY"),
		S3Bucket:      os.Getenv("S3_BUCKET"),
		S3UseSSL:      os.Getenv("S3_USE_SSL") == "true",
		CDNBaseURL:    os.Getenv("CDN_BASE_URL"),
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < minJWTSecretLen {
		return nil, fmt.Errorf("config: JWT_SECRET must be at least %d bytes", minJWTSecretLen)
	}
	cfg.TranscodeWorkers = 2
	if raw := os.Getenv("TRANSCODE_WORKERS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("config: TRANSCODE_WORKERS must be a positive integer")
		}
		cfg.TranscodeWorkers = n
	}
	cfg.TranscodeTimeout = 5 * time.Minute
	if raw := os.Getenv("TRANSCODE_TIMEOUT_SECONDS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("config: TRANSCODE_TIMEOUT_SECONDS must be a positive integer")
		}
		cfg.TranscodeTimeout = time.Duration(n) * time.Second
	}
	for name, v := range map[string]string{
		"S3_ENDPOINT":   cfg.S3Endpoint,
		"S3_ACCESS_KEY": cfg.S3AccessKey,
		"S3_SECRET_KEY": cfg.S3SecretKey,
		"S3_BUCKET":     cfg.S3Bucket,
		"CDN_BASE_URL":  cfg.CDNBaseURL,
	} {
		if v == "" {
			return nil, fmt.Errorf("config: %s is required", name)
		}
	}
	return cfg, nil
}
