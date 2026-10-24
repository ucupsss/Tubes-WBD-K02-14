//go:build integration

package session_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/session"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/testutil"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	pool   *pgxpool.Pool
	mgr    *session.Manager
	clk    *fakeClock
	userID int64
}

func setup(t *testing.T) env {
	t.Helper()
	pool := testutil.NewPlatformDB(t)
	uid := testutil.CreateUser(t, pool, "alice", false)
	// timestamptz menyimpan mikrodetik; jam palsu dibulatkan supaya nilai dari DB sama persis.
	clk := &fakeClock{t: time.Now().Truncate(time.Microsecond)}
	users := auth.NewService(pool, auth.NewPgRepo())
	mgr := session.NewManager(pool, session.NewPgRepo(), users, clk, session.DefaultConfig())
	return env{pool: pool, mgr: mgr, clk: clk, userID: uid}
}

func (e env) login(t *testing.T) (string, session.Session) {
	t.Helper()
	u, err := auth.NewService(e.pool, auth.NewPgRepo()).LookupUser(context.Background(), e.userID)
	if err != nil {
		t.Fatal(err)
	}
	token, s, err := e.mgr.Create(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return token, s
}

func (e env) resolve(t *testing.T, token string) session.Result {
	t.Helper()
	res, err := e.mgr.Resolve(context.Background(), token)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return res
}

func (e env) mustBeInvalid(t *testing.T, token, why string) {
	t.Helper()
	if _, err := e.mgr.Resolve(context.Background(), token); !session.IsInvalid(err) {
		t.Fatalf("%s: err = %v, want ErrInvalid", why, err)
	}
}

func TestPg_OnlyHashStored(t *testing.T) {
	e := setup(t)
	token, s := e.login(t)

	var stored []byte
	var csrf string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT token_hash, csrf_token FROM sessions WHERE id = $1`, s.ID).Scan(&stored, &csrf); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(token))
	if !bytes.Equal(stored, want[:]) || bytes.Contains(stored, []byte(token)) {
		t.Fatal("DB hanya boleh menyimpan SHA-256 token (D4)")
	}
	if csrf == "" || csrf == token {
		t.Fatal("csrf_token harus terisi dan berbeda dari token session")
	}

	if res := e.resolve(t, token); res.User.ID != e.userID || res.NewToken != "" {
		t.Fatalf("session baru: %+v", res)
	}
	e.mustBeInvalid(t, "token-yang-tidak-ada", "token asing")
}

func TestPg_RotationGraceAndAbsolute(t *testing.T) {
	e := setup(t)
	oldTok, s := e.login(t)

	e.clk.Advance(15 * time.Minute)
	res := e.resolve(t, oldTok)
	newTok := res.NewToken
	if newTok == "" || newTok == oldTok {
		t.Fatal("setelah 15 menit session harus dirotasi")
	}

	// Token lama masih diterima selama grace, tanpa rotasi lagi (D5).
	e.clk.Advance(29 * time.Second)
	if res := e.resolve(t, oldTok); res.NewToken != "" || res.Session.ID != s.ID {
		t.Fatalf("token grace: %+v", res)
	}

	e.clk.Advance(2 * time.Second)
	e.mustBeInvalid(t, oldTok, "token lama setelah grace")
	e.resolve(t, newTok)

	var abs time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT absolute_expires_at FROM sessions WHERE id = $1`, s.ID).Scan(&abs); err != nil {
		t.Fatal(err)
	}
	if !abs.Equal(s.AbsoluteExpiresAt) {
		t.Fatalf("rotasi tidak boleh memperpanjang absolute: %v → %v", s.AbsoluteExpiresAt, abs)
	}
}

func TestPg_AbsoluteExpiry(t *testing.T) {
	e := setup(t)
	token, s := e.login(t)

	// Aktif terus tiap 10 menit (tidak pernah idle) tetap mati di jam ke-8.
	for e.clk.Now().Add(10 * time.Minute).Before(s.AbsoluteExpiresAt) {
		e.clk.Advance(10 * time.Minute)
		if res := e.resolve(t, token); res.NewToken != "" {
			token = res.NewToken
		}
	}
	e.clk.Advance(10 * time.Minute)
	e.mustBeInvalid(t, token, "lewat absolute timeout")
}

func TestPg_IdleTimeout(t *testing.T) {
	e := setup(t)
	token, _ := e.login(t)

	// Seperti browser: selalu pakai token terbaru bila server merotasi.
	visit := func(after time.Duration) {
		t.Helper()
		e.clk.Advance(after)
		if res := e.resolve(t, token); res.NewToken != "" {
			token = res.NewToken
		}
	}

	// Aktivitas di menit 20 (rotasi, memperbarui last_seen_at) membuat menit 40 masih valid.
	visit(20 * time.Minute)
	visit(20 * time.Minute)

	// Aktivitas tanpa rotasi (touch, dibatasi 1x/menit) juga memperbarui last_seen_at:
	// menit 42 → touch; menit 71 masih valid karena baru 29 menit sejak menit 42.
	visit(2 * time.Minute)
	var rotatedAt, lastSeen time.Time
	if err := e.pool.QueryRow(context.Background(),
		`SELECT rotated_at, last_seen_at FROM sessions WHERE user_id = $1`, e.userID).Scan(&rotatedAt, &lastSeen); err != nil {
		t.Fatal(err)
	}
	if !lastSeen.Equal(e.clk.Now()) || rotatedAt.Equal(lastSeen) {
		t.Fatalf("menit 42 harus touch tanpa rotasi: rotated_at=%v last_seen_at=%v", rotatedAt, lastSeen)
	}
	visit(29 * time.Minute)

	e.clk.Advance(31 * time.Minute)
	e.mustBeInvalid(t, token, "idle 31 menit")
}

func TestPg_RevokeAndRevokeAll(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	tokA, sA := e.login(t)
	e.clk.Advance(15 * time.Minute)
	newA := e.resolve(t, tokA).NewToken
	if err := e.mgr.Revoke(ctx, sA.ID); err != nil {
		t.Fatal(err)
	}
	e.mustBeInvalid(t, newA, "token current setelah logout")
	e.mustBeInvalid(t, tokA, "token grace setelah logout")

	tok1, _ := e.login(t)
	tok2, _ := e.login(t)
	if err := e.mgr.RevokeAllForUser(ctx, e.userID); err != nil {
		t.Fatal(err)
	}
	e.mustBeInvalid(t, tok1, "session 1 setelah RevokeAllForUser")
	e.mustBeInvalid(t, tok2, "session 2 setelah RevokeAllForUser")
}

func TestPg_InactiveUserRejected(t *testing.T) {
	e := setup(t)
	token, _ := e.login(t)
	if _, err := e.pool.Exec(context.Background(), `UPDATE users SET is_active = false WHERE id = $1`, e.userID); err != nil {
		t.Fatal(err)
	}
	e.mustBeInvalid(t, token, "user nonaktif")
}

func TestPg_PrivilegeBumpRotates(t *testing.T) {
	e := setup(t)
	token, s := e.login(t)

	if err := auth.NewPrivilegeBumper(e.pool).BumpPrivilege(context.Background(), e.userID); err != nil {
		t.Fatal(err)
	}
	res := e.resolve(t, token)
	if res.NewToken == "" {
		t.Fatal("privilege naik harus memicu rotasi (D6)")
	}

	var sessVer, userVer int32
	if err := e.pool.QueryRow(context.Background(), `
		SELECT s.privilege_version, u.privilege_version
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = $1`, s.ID).Scan(&sessVer, &userVer); err != nil {
		t.Fatal(err)
	}
	if sessVer != userVer || sessVer != s.PrivilegeVersion+1 {
		t.Fatalf("privilege_version session=%d user=%d, want keduanya %d", sessVer, userVer, s.PrivilegeVersion+1)
	}
	if res := e.resolve(t, res.NewToken); res.NewToken != "" {
		t.Fatal("setelah sinkron tidak boleh rotasi lagi")
	}
}

// Dua request yang sama-sama membaca token current lalu merotasi: hanya yang pertama boleh berhasil,
// kalau tidak, token hasil rotasi pertama langsung tertimpa dan browser pertama ter-logout.
func TestPg_RotateIsConditional(t *testing.T) {
	e := setup(t)
	_, s := e.login(t)
	repo := session.NewPgRepo()
	ctx := context.Background()
	now := e.clk.Now()

	in := session.RotateInput{
		ID: s.ID, OldHash: s.TokenHash, NewHash: bytes.Repeat([]byte{1}, 32),
		PrevValidUntil: now.Add(30 * time.Second), Now: now, PrivilegeVersion: s.PrivilegeVersion,
	}
	if ok, err := repo.Rotate(ctx, e.pool, in); err != nil || !ok {
		t.Fatalf("rotasi pertama: %v, %v", ok, err)
	}
	in.NewHash = bytes.Repeat([]byte{2}, 32)
	if ok, err := repo.Rotate(ctx, e.pool, in); err != nil || ok {
		t.Fatalf("rotasi kedua dengan token lama: rotated=%v err=%v, want false", ok, err)
	}

	var current []byte
	if err := e.pool.QueryRow(ctx, `SELECT token_hash FROM sessions WHERE id = $1`, s.ID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, bytes.Repeat([]byte{1}, 32)) {
		t.Fatal("hasil rotasi pertama tidak boleh tertimpa")
	}

	if err := e.mgr.Revoke(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	in.OldHash, in.NewHash = current, bytes.Repeat([]byte{3}, 32)
	if ok, _ := repo.Rotate(ctx, e.pool, in); ok {
		t.Fatal("session yang sudah di-revoke tidak boleh dirotasi")
	}
}

func TestPg_ConcurrentRotation(t *testing.T) {
	e := setup(t)
	token, _ := e.login(t)
	e.clk.Advance(15 * time.Minute)

	const n = 8
	var wg sync.WaitGroup
	results := make([]session.Result, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = e.mgr.Resolve(context.Background(), token)
		}()
	}
	wg.Wait()

	rotated := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("request paralel %d gagal: %v", i, errs[i])
		}
		if results[i].NewToken != "" {
			rotated++
		}
	}
	if rotated != 1 {
		t.Fatalf("rotasi terjadi %d kali, want tepat 1", rotated)
	}
}
