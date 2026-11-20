package explorer

import (
	"context"
	"fmt"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
)

type Service struct {
	conns      dbconn.Manager
	authz      authz.Authorizer
	repo       Repo
	connGetter dbconn.ConnectionGetter
}

func NewService(conns dbconn.Manager, a authz.Authorizer, repo Repo, cg dbconn.ConnectionGetter) *Service {
	return &Service{
		conns:      conns,
		authz:      a,
		repo:       repo,
		connGetter: cg,
	}
}

// GetDashboard mengambil daftar seluruh target database yang dapat diakses oleh user.
func (s *Service) GetDashboard(ctx context.Context, userID int64) ([]authz.DatabaseAccess, error) {
	dbs, err := s.authz.AccessibleDatabases(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("explorer.GetDashboard: %w", err)
	}
	return dbs, nil
}

// EffectiveLevel mengambil level akses efektif pengguna pada database tertentu.
func (s *Service) EffectiveLevel(ctx context.Context, userID, dbID int64) (authz.Level, error) {
	if s.authz == nil {
		return authz.LevelView, nil
	}
	return s.authz.EffectiveLevel(ctx, userID, dbID)
}

// GetDatabaseInfo mengambil detail koneksi target database untuk judul & breadcrumb.
func (s *Service) GetDatabaseInfo(ctx context.Context, dbID int64) (dbconn.Connection, error) {
	if s.connGetter == nil {
		return dbconn.Connection{ID: dbID, Name: fmt.Sprintf("Database #%d", dbID)}, nil
	}
	conn, err := s.connGetter.GetByID(ctx, dbID)
	if err != nil {
		return dbconn.Connection{ID: dbID, Name: fmt.Sprintf("Database #%d", dbID)}, nil
	}
	return conn, nil
}

// ListTables mengambil daftar seluruh tabel pada target database yang diinspeksi.
func (s *Service) ListTables(ctx context.Context, dbID int64) ([]TableSummary, error) {
	pool, err := s.conns.Pool(ctx, dbID)
	if err != nil {
		return nil, apperr.Unavailable("Database target tidak dapat dihubungi", err)
	}

	tables, err := s.repo.ListTables(ctx, pool)
	if err != nil {
		return nil, apperr.Unavailable("Gagal mengambil skema database target", err)
	}
	return tables, nil
}

// GetTableMetadata mengambil rincian struktur kolom, constraint, dan indeks tabel target.
func (s *Service) GetTableMetadata(ctx context.Context, dbID int64, schema, table string) (*TableMetadata, error) {
	pool, err := s.conns.Pool(ctx, dbID)
	if err != nil {
		return nil, apperr.Unavailable("Database target tidak dapat dihubungi", err)
	}

	exists, err := s.repo.TableExists(ctx, pool, schema, table)
	if err != nil {
		return nil, apperr.Unavailable("Gagal memvalidasi keberadaan tabel", err)
	}
	if !exists {
		return nil, ErrTableNotFound
	}

	cols, err := s.repo.GetColumns(ctx, pool, schema, table)
	if err != nil {
		return nil, fmt.Errorf("GetTableMetadata columns: %w", err)
	}

	cons, err := s.repo.GetConstraints(ctx, pool, schema, table)
	if err != nil {
		return nil, fmt.Errorf("GetTableMetadata constraints: %w", err)
	}

	idxs, err := s.repo.GetIndexes(ctx, pool, schema, table)
	if err != nil {
		return nil, fmt.Errorf("GetTableMetadata indexes: %w", err)
	}

	return &TableMetadata{
		DatabaseID:  dbID,
		Schema:      schema,
		Table:       table,
		Columns:     cols,
		Constraints: cons,
		Indexes:     idxs,
	}, nil
}

// GetTableData mengambil data baris tabel berpaginasi dari target database.
func (s *Service) GetTableData(ctx context.Context, dbID int64, schema, table string, page, size int) (*TableDataPage, error) {
	pool, err := s.conns.Pool(ctx, dbID)
	if err != nil {
		return nil, apperr.Unavailable("Database target tidak dapat dihubungi", err)
	}

	exists, err := s.repo.TableExists(ctx, pool, schema, table)
	if err != nil {
		return nil, apperr.Unavailable("Gagal memvalidasi keberadaan tabel", err)
	}
	if !exists {
		return nil, ErrTableNotFound
	}

	dataPage, err := s.repo.GetTableData(ctx, pool, schema, table, page, size)
	if err != nil {
		return nil, fmt.Errorf("GetTableData: %w", err)
	}

	dataPage.DatabaseID = dbID
	return dataPage, nil
}
