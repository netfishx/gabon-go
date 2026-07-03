// Package config 从环境变量装载配置，缺失即 fail fast。
package config

import (
	"fmt"
	"os"
)

const minJWTSecretLen = 32

type Config struct {
	DatabaseURL string
	JWTSecret   []byte
	HTTPAddr    string
	// AdminUsername/AdminPassword 为初始管理员引导凭据，仅在 admins 表为空时使用。
	AdminUsername string
	AdminPassword string
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		JWTSecret:     []byte(os.Getenv("JWT_SECRET")),
		HTTPAddr:      os.Getenv("HTTP_ADDR"),
		AdminUsername: os.Getenv("ADMIN_USERNAME"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
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
	return cfg, nil
}
