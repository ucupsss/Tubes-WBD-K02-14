// Program server adalah API HTTP dan penyaji SPA. Ia tidak menjalankan SQL migration di database target.
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

	"github.com/ucupsss/Tubes-WBD-K02-14/db"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
	"github.com/ucupsss/Tubes-WBD-K02-14/web"
)

func main() {
	cfg := config.MustLoad()
	logOut, closeLogs := telemetry.LogOutput(os.Stdout, "dbmp-server")
	defer func() { _ = closeLogs() }()
	logger := logx.New(logOut, slog.LevelInfo)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTelemetry, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName: "dbmp-server",
		Disabled:    os.Getenv("OTEL_TRACES_EXPORTER") == "none",
	})
	if err != nil {
		logger.Error("telemetry init", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(shutCtx)
	}()
	if err := platformdb.Migrate(cfg.PlatformDatabaseURL, db.PlatformMigrations); err != nil {
		logger.Error("platform migrate", "err", err)
		os.Exit(1)
	}
	pool, err := platformdb.Open(ctx, cfg.PlatformDatabaseURL)
	if err != nil {
		logger.Error("platform open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if _, err := auth.NewService(pool, auth.NewPgRepo()).EnsureBootstrapAdmin(ctx, cfg.BootstrapAdminUsername, cfg.BootstrapAdminPassword); err != nil {
		logger.Error("bootstrap admin", "err", err)
		os.Exit(1)
	}

	a, err := newApp(cfg, pool, web.FS, logger)
	if err != nil {
		logger.Error("load templates", "err", err)
		os.Exit(1)
	}
	defer a.Conns.CloseAll()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			logger.Error("server shutdown", "err", err)
		}
	}()

	logger.Info("server started", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
