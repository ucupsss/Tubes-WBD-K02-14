package testutil

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/httpx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/view"
)

// Server is an in-process migration app with real authorization and no session middleware.
type Server struct {
	Handler http.Handler
	Service *migration.Service
	Secret  []byte
	Pool    *pgxpool.Pool
}

// NewServer mounts migration routes behind authz.Protect.
// PostAs/GetAs place the principal on the request context, so tests do not need package session.
func NewServer(t *testing.T, pool *pgxpool.Pool) *Server {
	t.Helper()
	secret := bytes.Repeat([]byte{0x2a}, 32)
	getter := connGetter{db: pool, repo: databases.NewPgRepo()}
	mgr := dbconn.NewManager(getter, secret)
	t.Cleanup(mgr.CloseAll)

	az := authz.NewAuthorizer(dbconn.NewGrantRepo(pool))
	repo := migration.NewPgRepo()
	approvalSvc := approval.NewService(pool, approval.PoolTx{Pool: pool}, approval.NewPgRepo(), repo)
	svc := migration.NewService(pool, repo, approvalSvc)
	views := view.New()
	h := migration.NewHandler(svc, az, approvalSvc)
	mux := http.NewServeMux()
	mw := authz.NewMiddleware(az, func(w http.ResponseWriter, r *http.Request, err error) {
		httpx.HandleError(w, r, views, err)
	})
	migration.RegisterRoutes(mux, h, mw)
	return &Server{Handler: mux, Service: svc, Secret: secret, Pool: pool}
}

// PostAs sends a form POST as principal.
func (s *Server) PostAs(t *testing.T, p authz.Principal, path string, form url.Values) *http.Response {
	t.Helper()
	return s.do(t, http.MethodPost, p, path, form)
}

// GetAs sends a GET as principal.
func (s *Server) GetAs(t *testing.T, p authz.Principal, path string) *http.Response {
	t.Helper()
	return s.do(t, http.MethodGet, p, path, nil)
}

func (s *Server) do(t *testing.T, method string, p authz.Principal, path string, form url.Values) *http.Response {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req = req.WithContext(authz.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	return rec.Result()
}

type connGetter struct {
	db   platformdb.DBTX
	repo *databases.PgRepo
}

func (g connGetter) GetByID(ctx context.Context, id int64) (dbconn.Connection, error) {
	c, err := g.repo.GetByID(ctx, g.db, id)
	if err != nil {
		if errors.Is(err, databases.ErrNotFound) {
			return dbconn.Connection{}, dbconn.ErrNotFound
		}
		return dbconn.Connection{}, err
	}
	return c, nil
}
