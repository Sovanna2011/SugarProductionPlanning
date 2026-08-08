// Package config reads the server configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the server configuration.
type Config struct {
	// DatabaseURL is the PostgreSQL connection string.
	DatabaseURL string
	// Addr is the listen address, e.g. ":8080".
	Addr string
	// WebDir, when set, serves the UI5 dashboard from the same process.
	WebDir string
	// AutoMigrate applies pending migrations on start.
	AutoMigrate bool
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration
}

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:     env("SPP_DATABASE_URL", ""),
		Addr:            env("SPP_ADDR", ":8080"),
		WebDir:          env("SPP_WEB_DIR", ""),
		AutoMigrate:     env("SPP_AUTO_MIGRATE", "true") == "true",
		LogLevel:        env("SPP_LOG_LEVEL", "info"),
		ShutdownTimeout: 15 * time.Second,
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return c, fmt.Errorf("SPP_DATABASE_URL is required, for example postgres://user:pass@localhost:5432/spp")
	}
	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
