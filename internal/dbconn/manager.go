package dbconn

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/telemetry"
)

// ConnectionGetter mendefinisikan interface minimal untuk mengambil detail koneksi dari repo.
type ConnectionGetter interface {
	GetByID(ctx context.Context, id int64) (Connection, error)
}

// Manager bertanggung jawab mengelola connection pool (*pgxpool.Pool) untuk setiap target database.
// Pool dibuat secara lazy, di-cache, dan di-invalidate ketika konfigurasi koneksi berubah.
type Manager interface {
	Pool(ctx context.Context, dbID int64) (*pgxpool.Pool, error)
	Invalidate(dbID int64)
	CloseAll()
	TestConnection(ctx context.Context, conn Connection, plainPassword string) error
}

// DefaultManager adalah implementasi bawaan dari interface Manager.
type DefaultManager struct {
	repo      ConnectionGetter
	secretKey []byte
	pools     map[int64]*pgxpool.Pool
	mu        sync.RWMutex
	tracer    pgx.QueryTracer
}

// SetTracer memasang tracer pgx pada pool yang dibuat setelah pemanggilan ini.
// Nil mematikan tracer. Dipakai bila paket telemetry menyediakan PgxTracer.
func (m *DefaultManager) SetTracer(t pgx.QueryTracer) {
	m.mu.Lock()
	m.tracer = t
	m.mu.Unlock()
}

// Pools mengembalikan salinan daftar pool target yang sedang terbuka. Dipakai metrik pool koneksi.
func (m *DefaultManager) Pools() []*pgxpool.Pool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]*pgxpool.Pool, 0, len(m.pools))
	for _, p := range m.pools {
		list = append(list, p)
	}
	return list
}

// NewManager membuat instance DefaultManager baru.
func NewManager(repo ConnectionGetter, secretKey []byte) *DefaultManager {
	return &DefaultManager{
		repo:      repo,
		secretKey: secretKey,
		pools:     make(map[int64]*pgxpool.Pool),
	}
}

// Pool mengembalikan *pgxpool.Pool untuk dbID tertentu.
// Jika belum ada di cache, pool akan dibuat baru secara lazy dan divalidasi dengan Ping.
func (m *DefaultManager) Pool(ctx context.Context, dbID int64) (*pgxpool.Pool, error) {
	// 1. Cek cache (read lock)
	m.mu.RLock()
	pool, exists := m.pools[dbID]
	m.mu.RUnlock()

	if exists {
		// Verifikasi koneksi masih sehat
		pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
		defer pingCancel()
		if err := pool.Ping(pingCtx); err == nil {
			return pool, nil
		}
		// Jangan meng-invalidate pool yang sehat hanya karena context request pemanggil dibatalkan.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Jika ping gagal karena koneksi target bermasalah, invalidasi pool lama
		m.Invalidate(dbID)
	}

	// 2. Ambil metadata & buka pool baru di luar global write lock supaya satu DB yang lambat/mati
	// tidak memblokir request ke database lain.
	conn, err := m.repo.GetByID(ctx, dbID)
	if err != nil {
		return nil, err // ErrNotFound atau err internal
	}

	plainPassword, err := Decrypt(conn.PasswordEnc, m.secretKey)
	if err != nil {
		return nil, fmt.Errorf("gagal mendekripsi password koneksi: %w", err)
	}

	dsn := conn.BuildDSN(string(plainPassword))
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, ErrUnavailable
	}

	m.mu.RLock()
	tracer := m.tracer
	m.mu.RUnlock()
	if tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	} else {
		cfg.ConnConfig.Tracer = telemetry.PgxTracer()
	}

	// Konfigurasi pool standar
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = 30 * time.Minute

	connectCtx, connectCancel := context.WithTimeout(ctx, 5*time.Second)
	defer connectCancel()

	newPool, err := pgxpool.NewWithConfig(connectCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: tidak dapat membuat pool", ErrUnavailable)
	}

	// Pastikan ping berhasil
	if err := newPool.Ping(connectCtx); err != nil {
		newPool.Close()
		return nil, fmt.Errorf("%w: koneksi gagal merespons", ErrUnavailable)
	}

	// 3. Simpan ke cache dengan double-check (tutup pool ekstra jika goroutine lain lebih dulu selesai).
	m.mu.Lock()
	if existing, ok := m.pools[dbID]; ok {
		m.mu.Unlock()
		newPool.Close()
		return existing, nil
	}
	m.pools[dbID] = newPool
	m.mu.Unlock()
	return newPool, nil
}

// Invalidate menutup dan menghapus pool target database dari cache memory.
func (m *DefaultManager) Invalidate(dbID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if pool, exists := m.pools[dbID]; exists {
		pool.Close()
		delete(m.pools, dbID)
	}
}

// CloseAll menutup semua connection pool yang aktif. Dipanggil saat server shutdown.
func (m *DefaultManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, pool := range m.pools {
		pool.Close()
		delete(m.pools, id)
	}
}

// TestConnection menguji apakah konfigurasi koneksi target DB valid dan dapat dihubungi.
// Menggunakan batas waktu (timeout) 5 detik dan tidak membocorkan detail DSN/password.
func (m *DefaultManager) TestConnection(ctx context.Context, conn Connection, plainPassword string) error {
	dsn := conn.BuildDSN(plainPassword)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("%w: parameter koneksi tidak valid", ErrUnavailable)
	}

	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	testPool, err := pgxpool.NewWithConfig(testCtx, cfg)
	if err != nil {
		return fmt.Errorf("%w: gagal membuka koneksi", ErrUnavailable)
	}
	defer testPool.Close()

	if err := testPool.Ping(testCtx); err != nil {
		return fmt.Errorf("%w: koneksi ditolak atau timeout", ErrUnavailable)
	}

	return nil
}
