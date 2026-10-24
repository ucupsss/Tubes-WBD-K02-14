package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/ucupsss/Tubes-WBD-K02-14/db"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/clock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
	"github.com/ucupsss/Tubes-WBD-K02-14/web"
)

func main() {
	cfg := config.MustLoad()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
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

	views := view.MustNew(web.FS)
	// csrf.Token is not exported yet. views.SetCSRF(...) goes here when package csrf provides it.

	dbRepo := databases.NewPgRepo()
	mgr := dbconn.NewManager(connGetter{db: pool, repo: dbRepo}, cfg.SecretKey)
	defer mgr.CloseAll()

	az := authz.NewAuthorizer(dbconn.NewGrantRepo(pool))
	mw := authz.NewMiddleware(az, func(w http.ResponseWriter, r *http.Request, err error) {
		httpx.HandleError(w, r, views, err)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(web.FS))

	databases.RegisterRoutes(mux, databases.NewHandler(databases.NewService(pool, dbRepo, mgr, cfg.SecretKey), views), mw)
	// PrivilegeBumper is A's. nil skips the bump until that implementation is wired.
	access.RegisterRoutes(mux, access.NewHandler(access.NewService(pool, access.NewPgRepo(), nil), views), mw)

	migSvc := migration.NewService(pool, migration.NewPgRepo(), mgr, clock.Real{})
	migration.RegisterRoutes(mux, migration.NewHandler(migSvc, views, az), mw)

	// session.Load and csrf.Verify wrap the mux once package session and csrf export them.
	handler := httpx.Chain(mux,
		httpx.Logger(logger),
		httpx.Recover(logger),
		httpx.MaxBytes(httpx.MaxBodyBytes),
	)
	logger.Info("server started", "addr", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, handler); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
