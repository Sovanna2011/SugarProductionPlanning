// Package config reads the server configuration from the environment.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/httpapi"
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
	// Auth selects how requests are authenticated. Defaults to none, which
	// preserves the original behaviour and is only safe on a trusted network.
	Auth auth.Config
	// OverrideRole, when set, is the role required to force a posting that
	// capacity validation blocked. Empty leaves overrides open to anyone.
	OverrideRole string
	// TrustedProxies are the hops whose X-Forwarded-For header may be
	// believed. Empty means the header is ignored entirely.
	TrustedProxies []*net.IPNet
	// LoginMaxFailures is how many rejected sign-ins one client address may
	// make inside LoginFailureWindow before it is turned away. Zero disables
	// the limit.
	LoginMaxFailures int
	// LoginFailureWindow is the period LoginMaxFailures applies over.
	LoginFailureWindow time.Duration
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
		OverrideRole:    env("SPP_OVERRIDE_ROLE", ""),
		Auth: auth.Config{
			UserHeader:  env("SPP_AUTH_USER_HEADER", "X-Forwarded-User"),
			NameHeader:  env("SPP_AUTH_NAME_HEADER", "X-Forwarded-Name"),
			RolesHeader: env("SPP_AUTH_ROLES_HEADER", "X-Forwarded-Groups"),
			CookieName:  env("SPP_SESSION_COOKIE", "spp_session"),
			// Off by default because the evaluation deployment is plain HTTP,
			// and a Secure cookie there is silently discarded by the browser,
			// which looks exactly like a broken login. Turn it on — it must be
			// on — wherever the site is served over HTTPS.
			CookieSecure: env("SPP_SESSION_COOKIE_SECURE", "false") == "true",
		},
	}

	mode, ok := auth.ParseMode(env("SPP_AUTH_MODE", string(auth.ModeNone)))
	if !ok {
		return c, fmt.Errorf("SPP_AUTH_MODE must be none, local or proxy, got %q", os.Getenv("SPP_AUTH_MODE"))
	}
	c.Auth.Mode = mode

	ttl, err := time.ParseDuration(env("SPP_SESSION_TTL", "12h"))
	if err != nil || ttl <= 0 {
		return c, fmt.Errorf("SPP_SESSION_TTL must be a positive duration such as 12h, got %q",
			os.Getenv("SPP_SESSION_TTL"))
	}
	c.Auth.SessionTTL = ttl

	maxLifetime, err := time.ParseDuration(env("SPP_SESSION_MAX_LIFETIME", "168h"))
	if err != nil || maxLifetime <= 0 {
		return c, fmt.Errorf("SPP_SESSION_MAX_LIFETIME must be a positive duration such as 168h, got %q",
			os.Getenv("SPP_SESSION_MAX_LIFETIME"))
	}
	if maxLifetime < ttl {
		return c, fmt.Errorf("SPP_SESSION_MAX_LIFETIME (%s) is shorter than SPP_SESSION_TTL (%s), "+
			"which would end every session before its idle window", maxLifetime, ttl)
	}
	c.Auth.SessionMaxLifetime = maxLifetime

	// Behind a reverse proxy every request appears to come from the proxy, so
	// a per-client limit would become one global limit that the first attacker
	// closes for everybody. Naming the proxy is what avoids that.
	proxies, err := httpapi.ParseTrustedProxies(env("SPP_TRUSTED_PROXIES", ""))
	if err != nil {
		return c, fmt.Errorf("SPP_TRUSTED_PROXIES must be a comma-separated list of "+
			"addresses or CIDR blocks, such as 10.0.0.7,172.16.0.0/12: %w", err)
	}
	c.TrustedProxies = proxies

	maxFailures, err := strconv.Atoi(env("SPP_LOGIN_MAX_FAILURES", "15"))
	if err != nil || maxFailures < 0 {
		return c, fmt.Errorf("SPP_LOGIN_MAX_FAILURES must be a non-negative whole number, got %q",
			os.Getenv("SPP_LOGIN_MAX_FAILURES"))
	}
	c.LoginMaxFailures = maxFailures

	failureWindow, err := time.ParseDuration(env("SPP_LOGIN_FAILURE_WINDOW", "15m"))
	if err != nil || failureWindow <= 0 {
		return c, fmt.Errorf("SPP_LOGIN_FAILURE_WINDOW must be a positive duration such as 15m, got %q",
			os.Getenv("SPP_LOGIN_FAILURE_WINDOW"))
	}
	c.LoginFailureWindow = failureWindow

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
