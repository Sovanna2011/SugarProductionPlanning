// Command server runs the Sugar Production Planning API and, optionally,
// serves the SAP UI5 dashboard alongside it.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/config"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/httpapi"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/ratelimit"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
	"github.com/sovanna2011/sugarproductionplanning/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	log.Info("connected to database")

	if cfg.AutoMigrate {
		files, err := migrations.Load()
		if err != nil {
			return err
		}
		if err := store.Migrate(ctx, files); err != nil {
			return err
		}
		log.Info("migrations up to date", "count", len(files))
	}

	svc := service.New(store,
		service.WithOverrideRole(cfg.OverrideRole),
		service.WithSessionTTL(cfg.Auth.SessionTTL))
	if cfg.OverrideRole != "" {
		log.Info("capacity overrides restricted", "role", cfg.OverrideRole)
	}

	// A deployment that has not chosen an authentication mechanism is only
	// safe on a trusted network, and should be told so on every start.
	switch cfg.Auth.Mode {
	case auth.ModeNone:
		log.Warn("authentication is disabled: every endpoint is open and the audit trail " +
			"records whatever the caller declares. Safe only on a trusted network. " +
			"See docs/security.md")

	case auth.ModeLocal:
		// The service resolves session tokens, so the middleware can stay
		// free of the database.
		cfg.Auth.Sessions = svc

		n, demo, err := store.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			log.Warn("sign-in is required but no accounts exist, so nobody can sign in. " +
				"Create one with: spp-seed-users -admin <name>")
		}
		if demo > 0 {
			log.Warn("this database holds demo accounts whose passwords are published in the "+
				"project documentation. Deactivate them before the system holds anything real: "+
				"spp-seed-users -remove-demo", "accounts", demo)
		}
		log.Info("authentication enabled", "mode", cfg.Auth.Mode,
			"users", n, "sessionTtl", cfg.Auth.SessionTTL, "secureCookie", cfg.Auth.CookieSecure)
		if !cfg.Auth.CookieSecure {
			log.Warn("the session cookie is not marked Secure, so a browser will send it over " +
				"plain HTTP. Set SPP_SESSION_COOKIE_SECURE=true wherever this is served over HTTPS")
		}

	default:
		log.Info("authentication enabled", "mode", cfg.Auth.Mode, "userHeader", cfg.Auth.UserHeader)
	}

	opts := httpapi.Options{
		OverrideRole:   cfg.OverrideRole,
		TrustedProxies: cfg.TrustedProxies,
	}
	if cfg.Auth.Mode == auth.ModeLocal && cfg.LoginMaxFailures > 0 {
		// 10,000 tracked addresses is a few megabytes and far more than an
		// internal site has clients. A flood past it stops throttling new
		// addresses rather than evicting the counters already held, which is
		// what flooding it would be for.
		opts.LoginLimit = ratelimit.NewFailures(cfg.LoginMaxFailures, cfg.LoginFailureWindow, 10000)
		log.Info("failed sign-ins throttled",
			"perClient", cfg.LoginMaxFailures, "window", cfg.LoginFailureWindow)

		if len(cfg.TrustedProxies) == 0 {
			log.Info("X-Forwarded-For is ignored, so clients are identified by the address " +
				"they connect from. Behind a reverse proxy, set SPP_TRUSTED_PROXIES or every " +
				"request will look like it came from the proxy")
		}
	}

	api := httpapi.New(svc, log, cfg.WebDir, cfg.Auth, opts)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "webDir", cfg.WebDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
