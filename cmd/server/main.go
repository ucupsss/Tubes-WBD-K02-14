package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/ucupsss/Tubes-WBD-K02-14/db"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/web"
)

func main() {
	cfg := config.MustLoad()
	logger := logx.New(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)

	ctx := context.Background()
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

	logger.Info("server started", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, a.Handler); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
