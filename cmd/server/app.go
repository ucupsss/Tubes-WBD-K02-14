package main

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/groups"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/users"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/clock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/csrf"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explorer"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// app is the fully wired HTTP application. Tests build it the same way main does.
type app struct {
	Handler http.Handler
	Conns   *dbconn.DefaultManager
}

// newApp wires every module and the middleware chain:
// Logger → Recover → MaxBytes → session.Load → csrf.Verify → mux (authz.Protect per route).
func newApp(cfg config.Config, pool *pgxpool.Pool, webFS fs.FS, logger *slog.Logger) (*app, error) {
	views, err := view.NewFromFS(webFS)
	if err != nil {
		return nil, err
	}
	views.SetCSRF(csrf.Token)
	onErr := func(w http.ResponseWriter, r *http.Request, err error) {
		httpx.HandleError(w, r, views, err)
	}
	secure := cfg.AppEnv == "production"
	clk := clock.Real{}

	authSvc := auth.NewService(pool, auth.NewPgRepo())
	sessMgr := session.NewManager(pool, session.NewPgRepo(), authSvc, clk, session.Config{
		IdleTimeout:      cfg.SessionIdleTimeout,
		AbsoluteTimeout:  cfg.SessionAbsoluteTimeout,
		RotationInterval: cfg.SessionRotationInterval,
		GracePeriod:      cfg.SessionGracePeriod,
		Secure:           secure,
	})
	bumper := auth.NewPrivilegeBumper(pool)

	dbRepo := databases.NewPgRepo()
	getter := connGetter{db: pool, repo: dbRepo}
	conns := dbconn.NewManager(getter, cfg.SecretKey)

	az := authz.NewAuthorizer(dbconn.NewGrantRepo(pool))
	mw := authz.NewMiddleware(az, onErr)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /static/", http.FileServerFS(webFS))

	auth.RegisterRoutes(mux, auth.NewHandler(authSvc, sessMgr, views, logger), mw)
	users.RegisterRoutes(mux, users.NewHandler(users.NewService(pool, users.NewPgRepo(), authSvc, sessMgr), views), mw)
	groups.RegisterRoutes(mux, groups.NewHandler(groups.NewService(pool, groups.NewPgRepo(), bumper), views), mw)
	databases.RegisterRoutes(mux, databases.NewHandler(databases.NewService(pool, dbRepo, conns, cfg.SecretKey), views), mw)
	access.RegisterRoutes(mux, access.NewHandler(access.NewService(pool, access.NewPgRepo(), bumper), views), mw)
	explorer.RegisterRoutes(mux, explorer.NewHandler(explorer.NewService(conns, az, explorer.NewPgRepo(), getter), views), mw)
	migration.RegisterRoutes(mux, migration.NewHandler(migration.NewService(pool, migration.NewPgRepo(), conns, clk), views, az), mw)

	handler := httpx.Chain(mux,
		httpx.Logger(logger),
		httpx.Recover(logger),
		httpx.MaxBytes(httpx.MaxBodyBytes),
		session.NewMiddleware(sessMgr, onErr).Load,
		csrf.NewMiddleware(csrf.Config{Secure: secure}, onErr).Verify,
	)
	return &app{Handler: handler, Conns: conns}, nil
}
