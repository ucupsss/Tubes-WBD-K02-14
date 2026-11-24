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
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/api"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/clock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/csrf"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explain"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/explorer"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/spa"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// app is the fully wired HTTP application. Tests build it the same way main does.
type app struct {
	Handler http.Handler
	Conns   *dbconn.DefaultManager
}

// newApp wires every module and the middleware chain:
// Correlation → Logger → Recover → MaxBytes → session.Load → csrf.Verify → mux (authz.Protect per route).
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
	// Path yang tidak cocok dengan route server mana pun: API → 404 JSON; selain itu dijawab SPA (router
	// di browser yang menangani /login, /admin/..., dst.). Bila frontend belum di-build → halaman error bawaan.
	spaHandler := spa.New(webFS)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !httpx.IsAPI(r) && spaHandler.Serve(w, r) {
			return
		}
		httpx.HandleError(w, r, views, apperr.NotFound("Halaman tidak ditemukan"))
	})

	migRepo := migration.NewPgRepo()
	approvalSvc := approval.NewService(pool, approval.PoolTx{Pool: pool}, approval.NewPgRepo(), migRepo)
	groupsSvc := groups.NewService(pool, groups.NewPgRepo(), bumper)
	usersSvc := users.NewService(pool, users.NewPgRepo(), authSvc, sessMgr).WithLeaderGuard(groupsSvc)
	databasesSvc := databases.NewService(pool, dbRepo, conns, cfg.SecretKey)
	accessSvc := access.NewService(pool, access.NewPgRepo(), bumper)
	api.New(api.Deps{
		Auth: authSvc, Sessions: sessMgr, Users: usersSvc, Groups: groupsSvc, Access: accessSvc,
		Databases: databasesSvc, Approval: approvalSvc, Authz: az, Logger: logger,
	}).RegisterRoutes(mux, mw)

	auth.RegisterRoutes(mux, auth.NewHandler(authSvc, sessMgr, views, logger), mw)
	explorer.RegisterRoutes(mux, explorer.NewHandler(explorer.NewService(conns, az, explorer.NewPgRepo(), getter), views), mw)
	migSvc := migration.NewService(pool, migRepo, approvalSvc).WithQueue(queue.New(pool))
	migration.RegisterRoutes(mux, migration.NewHandler(migSvc, az, approvalSvc), mw)
	explain.RegisterRoutes(mux, explain.NewHandler(migSvc, explain.NewAnalyzer(conns), views), mw)

	handler := httpx.Chain(mux,
		logx.CorrelationMiddleware,
		httpx.Logger(logger),
		httpx.Recover(logger),
		httpx.MaxBytes(httpx.MaxBodyBytes),
		session.NewMiddleware(sessMgr, onErr).Load,
		csrf.NewMiddleware(csrf.Config{Secure: secure}, onErr).Verify,
	)
	return &app{Handler: handler, Conns: conns}, nil
}
