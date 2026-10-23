package session

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// --- test doubles ---

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// memRepo meniru semantik PgRepo (termasuk grace & rotasi bersyarat) di memori.
type memRepo struct {
	rows       map[int64]*Session
	nextID     int64
	failRotate bool // simulasi request paralel yang sudah merotasi duluan
	err        error
	touches    int
}

func newMemRepo() *memRepo { return &memRepo{rows: map[int64]*Session{}, nextID: 1} }

func (r *memRepo) Create(ctx context.Context, db platformdb.DBTX, s Session) (int64, error) {
	if r.err != nil {
		return 0, r.err
	}
	s.ID = r.nextID
	r.nextID++
	r.rows[s.ID] = &s
	return s.ID, nil
}

func (r *memRepo) FindByTokenHash(ctx context.Context, db platformdb.DBTX, hash []byte, now time.Time) (Session, error) {
	if r.err != nil {
		return Session{}, r.err
	}
	for _, s := range r.rows {
		current := bytes.Equal(s.TokenHash, hash)
		grace := bytes.Equal(s.PrevTokenHash, hash) && s.PrevValidUntil != nil && now.Before(*s.PrevValidUntil)
		if current || grace {
			return *s, nil
		}
	}
	return Session{}, ErrInvalid
}

func (r *memRepo) Rotate(ctx context.Context, db platformdb.DBTX, in RotateInput) (bool, error) {
	s := r.rows[in.ID]
	if r.failRotate || s == nil || !bytes.Equal(s.TokenHash, in.OldHash) || s.RevokedAt != nil {
		return false, nil
	}
	pv := in.PrevValidUntil
	s.PrevTokenHash, s.PrevValidUntil = s.TokenHash, &pv
	s.TokenHash, s.RotatedAt, s.LastSeenAt, s.PrivilegeVersion = in.NewHash, in.Now, in.Now, in.PrivilegeVersion
	return true, nil
}

func (r *memRepo) Touch(ctx context.Context, db platformdb.DBTX, id int64, now time.Time) error {
	r.touches++
	if s := r.rows[id]; s != nil && s.LastSeenAt.Before(now) {
		s.LastSeenAt = now
	}
	return nil
}

func (r *memRepo) Revoke(ctx context.Context, db platformdb.DBTX, id int64, now time.Time) error {
	if s := r.rows[id]; s != nil && s.RevokedAt == nil {
		s.RevokedAt = &now
	}
	return nil
}

func (r *memRepo) RevokeAllForUser(ctx context.Context, db platformdb.DBTX, userID int64, now time.Time) error {
	for _, s := range r.rows {
		if s.UserID == userID && s.RevokedAt == nil {
			t := now
			s.RevokedAt = &t
		}
	}
	return nil
}

type stubUsers struct {
	users map[int64]User
	err   error
}

func (s *stubUsers) LookupUser(ctx context.Context, id int64) (User, error) {
	if s.err != nil {
		return User{}, s.err
	}
	u, ok := s.users[id]
	if !ok {
		return User{}, apperr.NotFound("User tidak ditemukan")
	}
	return u, nil
}

var (
	t0      = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	errDB   = errors.New("platform db down")
	aliceID = int64(10)
	bobID   = int64(11)
)

type fixture struct {
	mgr   *Manager
	repo  *memRepo
	users *stubUsers
	clk   *fakeClock
	ctx   context.Context
}

func newFixture() *fixture {
	repo := newMemRepo()
	users := &stubUsers{users: map[int64]User{
		aliceID: {ID: aliceID, Username: "alice", IsActive: true, PrivilegeVersion: 1},
		bobID:   {ID: bobID, Username: "bob", IsActive: true, PrivilegeVersion: 1},
	}}
	clk := &fakeClock{t: t0}
	return &fixture{
		mgr:   NewManager(nil, repo, users, clk, DefaultConfig()),
		repo:  repo,
		users: users,
		clk:   clk,
		ctx:   context.Background(),
	}
}

func (f *fixture) login(t *testing.T, userID int64) (string, Session) {
	t.Helper()
	token, s, err := f.mgr.Create(f.ctx, f.users.users[userID])
	if err != nil {
		t.Fatal(err)
	}
	return token, s
}

// keepAlive memanggil Resolve tiap `step` sampai `total`, mengikuti token baru bila dirotasi.
func (f *fixture) keepAlive(t *testing.T, token string, step, total time.Duration) string {
	t.Helper()
	for elapsed := time.Duration(0); elapsed < total; elapsed += step {
		f.clk.Advance(step)
		res, err := f.mgr.Resolve(f.ctx, token)
		if err != nil {
			t.Fatalf("Resolve gagal setelah %v: %v", elapsed+step, err)
		}
		if res.NewToken != "" {
			token = res.NewToken
		}
	}
	return token
}

// --- token ---

func TestNewToken(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		tok, err := newToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 43 { // 32 byte base64url tanpa padding
			t.Fatalf("panjang token = %d, want 43", len(tok))
		}
		if seen[tok] {
			t.Fatal("token kembar")
		}
		seen[tok] = true
	}
}

func TestHashToken(t *testing.T) {
	h := hashToken("abc")
	if len(h) != 32 {
		t.Fatalf("panjang hash = %d, want 32 (SHA-256)", len(h))
	}
	if !bytes.Equal(h, hashToken("abc")) || bytes.Equal(h, hashToken("abd")) {
		t.Fatal("hash harus deterministik dan berbeda untuk input berbeda")
	}
}

// --- keputusan murni ---

func TestCheckValid(t *testing.T) {
	cfg := DefaultConfig()
	revoked := t0
	base := Session{LastSeenAt: t0, AbsoluteExpiresAt: t0.Add(cfg.AbsoluteTimeout)}

	tests := []struct {
		name  string
		mod   func(s *Session)
		now   time.Time
		valid bool
	}{
		{"fresh", nil, t0, true},
		{"idle exactly at limit", nil, t0.Add(cfg.IdleTimeout), true},
		{"idle exceeded", nil, t0.Add(cfg.IdleTimeout + time.Second), false},
		{"revoked", func(s *Session) { s.RevokedAt = &revoked }, t0, false},
		{"absolute reached", func(s *Session) { s.LastSeenAt = t0.Add(cfg.AbsoluteTimeout) }, t0.Add(cfg.AbsoluteTimeout), false},
		{"just before absolute", func(s *Session) { s.LastSeenAt = t0.Add(cfg.AbsoluteTimeout - time.Minute) }, t0.Add(cfg.AbsoluteTimeout - time.Second), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := base
			if tt.mod != nil {
				tt.mod(&s)
			}
			err := checkValid(s, cfg, tt.now)
			if (err == nil) != tt.valid {
				t.Fatalf("checkValid err = %v, want valid=%v", err, tt.valid)
			}
			if err != nil && !IsInvalid(err) {
				t.Fatalf("error harus ErrInvalid, got %v", err)
			}
		})
	}
}

func TestShouldRotate(t *testing.T) {
	cfg := DefaultConfig()
	s := Session{RotatedAt: t0, PrivilegeVersion: 1}

	tests := []struct {
		name     string
		grace    bool
		userPriv int32
		now      time.Time
		want     bool
	}{
		{"before interval", false, 1, t0.Add(cfg.RotationInterval - time.Second), false},
		{"at interval", false, 1, t0.Add(cfg.RotationInterval), true},
		{"privilege bumped", false, 2, t0.Add(time.Second), true},
		{"grace token after interval (D5)", true, 1, t0.Add(cfg.RotationInterval * 2), false},
		{"grace token with privilege bump (D5)", true, 2, t0.Add(time.Second), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRotate(s, tt.grace, tt.userPriv, cfg, tt.now); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Manager ---

func TestManager_Create(t *testing.T) {
	f := newFixture()
	token, s := f.login(t, aliceID)
	token2, s2 := f.login(t, aliceID)

	stored := f.repo.rows[s.ID]
	if bytes.Contains(stored.TokenHash, []byte(token)) || !bytes.Equal(stored.TokenHash, hashToken(token)) {
		t.Fatal("yang disimpan harus SHA-256 token, bukan token mentah")
	}
	if !stored.AbsoluteExpiresAt.Equal(t0.Add(8 * time.Hour)) {
		t.Fatalf("absolute = %v, want %v", stored.AbsoluteExpiresAt, t0.Add(8*time.Hour))
	}
	if s.CSRFToken == "" || s.CSRFToken == token {
		t.Fatal("CSRF token harus terisi dan berbeda dari token session")
	}
	if token == token2 || s.CSRFToken == s2.CSRFToken {
		t.Fatal("setiap login harus menghasilkan token & CSRF token baru")
	}
}

func TestManager_Create_RepoError(t *testing.T) {
	f := newFixture()
	f.repo.err = errDB
	if _, _, err := f.mgr.Create(f.ctx, f.users.users[aliceID]); !errors.Is(err, errDB) {
		t.Fatalf("err = %v, want %v", err, errDB)
	}
}

func TestManager_Resolve_Fresh(t *testing.T) {
	f := newFixture()
	token, s := f.login(t, aliceID)

	f.clk.Advance(5 * time.Second)
	res, err := f.mgr.Resolve(f.ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewToken != "" {
		t.Fatal("tidak boleh dirotasi sebelum interval")
	}
	if res.Session.ID != s.ID || res.User.Username != "alice" {
		t.Fatalf("hasil salah: %+v", res)
	}
}

func TestManager_Resolve_InvalidTokens(t *testing.T) {
	f := newFixture()
	f.login(t, aliceID)
	for _, tok := range []string{"", "tidak-ada", "x' OR '1'='1"} {
		if _, err := f.mgr.Resolve(f.ctx, tok); !IsInvalid(err) {
			t.Fatalf("token %q: err = %v, want ErrInvalid", tok, err)
		}
	}
}

func TestManager_Resolve_RotationAndGrace(t *testing.T) {
	f := newFixture()
	oldToken, s := f.login(t, aliceID)

	f.clk.Advance(15 * time.Minute)
	res, err := f.mgr.Resolve(f.ctx, oldToken)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewToken == "" || res.NewToken == oldToken {
		t.Fatal("token harus dirotasi setelah 15 menit")
	}
	newToken := res.NewToken

	// Token lama masih berlaku selama grace, tanpa rotasi lagi (D5).
	f.clk.Advance(29 * time.Second)
	res, err = f.mgr.Resolve(f.ctx, oldToken)
	if err != nil {
		t.Fatalf("token lama dalam grace harus diterima: %v", err)
	}
	if res.NewToken != "" {
		t.Fatal("token grace tidak boleh memicu rotasi lagi")
	}

	// Setelah grace habis, token lama ditolak; token baru tetap berlaku.
	f.clk.Advance(2 * time.Second)
	if _, err := f.mgr.Resolve(f.ctx, oldToken); !IsInvalid(err) {
		t.Fatalf("token lama setelah grace: err = %v, want ErrInvalid", err)
	}
	if _, err := f.mgr.Resolve(f.ctx, newToken); err != nil {
		t.Fatalf("token baru harus berlaku: %v", err)
	}

	if !f.repo.rows[s.ID].AbsoluteExpiresAt.Equal(s.AbsoluteExpiresAt) {
		t.Fatal("rotasi tidak boleh mengubah absolute expiration")
	}
}

func TestManager_Resolve_RotationDoesNotExtendAbsolute(t *testing.T) {
	f := newFixture()
	token, _ := f.login(t, aliceID)

	// Aktif terus tiap 10 menit (selalu < idle) sampai tepat sebelum 8 jam.
	token = f.keepAlive(t, token, 10*time.Minute, 8*time.Hour-10*time.Minute)

	f.clk.Advance(10 * time.Minute) // tepat 8 jam sejak login
	if _, err := f.mgr.Resolve(f.ctx, token); !IsInvalid(err) {
		t.Fatalf("setelah 8 jam: err = %v, want ErrInvalid (absolute)", err)
	}
}

func TestManager_Resolve_IdleTimeout(t *testing.T) {
	f := newFixture()
	token, _ := f.login(t, aliceID)

	f.clk.Advance(30*time.Minute + time.Second)
	if _, err := f.mgr.Resolve(f.ctx, token); !IsInvalid(err) {
		t.Fatalf("err = %v, want ErrInvalid (idle)", err)
	}
}

func TestManager_Resolve_ActivityResetsIdle(t *testing.T) {
	f := newFixture()
	token, _ := f.login(t, aliceID)

	token = f.keepAlive(t, token, 20*time.Minute, 60*time.Minute)
	if _, err := f.mgr.Resolve(f.ctx, token); err != nil {
		t.Fatalf("session aktif tidak boleh idle: %v", err)
	}
}

func TestManager_Resolve_PrivilegeBumpRotates(t *testing.T) {
	f := newFixture()
	token, s := f.login(t, aliceID)

	u := f.users.users[aliceID]
	u.PrivilegeVersion = 2
	f.users.users[aliceID] = u

	f.clk.Advance(time.Second)
	res, err := f.mgr.Resolve(f.ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewToken == "" {
		t.Fatal("privilege naik harus memicu rotasi (D6)")
	}
	if f.repo.rows[s.ID].PrivilegeVersion != 2 {
		t.Fatal("privilege_version session harus diperbarui setelah rotasi")
	}

	f.clk.Advance(time.Second)
	if res, _ := f.mgr.Resolve(f.ctx, res.NewToken); res.NewToken != "" {
		t.Fatal("setelah rotasi, privilege sama → tidak dirotasi lagi")
	}
}

func TestManager_Resolve_ConcurrentRotation(t *testing.T) {
	f := newFixture()
	token, _ := f.login(t, aliceID)
	f.repo.failRotate = true

	f.clk.Advance(16 * time.Minute)
	res, err := f.mgr.Resolve(f.ctx, token)
	if err != nil {
		t.Fatalf("kalah balapan rotasi tetap harus dilayani: %v", err)
	}
	if res.NewToken != "" {
		t.Fatal("tidak boleh Set-Cookie bila rotasi dilakukan request lain")
	}
}

func TestManager_Resolve_UserState(t *testing.T) {
	t.Run("inactive user", func(t *testing.T) {
		f := newFixture()
		token, _ := f.login(t, aliceID)
		u := f.users.users[aliceID]
		u.IsActive = false
		f.users.users[aliceID] = u
		if _, err := f.mgr.Resolve(f.ctx, token); !IsInvalid(err) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
	t.Run("user gone", func(t *testing.T) {
		f := newFixture()
		token, _ := f.login(t, aliceID)
		delete(f.users.users, aliceID)
		if _, err := f.mgr.Resolve(f.ctx, token); !IsInvalid(err) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
	t.Run("user lookup db error", func(t *testing.T) {
		f := newFixture()
		token, _ := f.login(t, aliceID)
		f.users.err = errDB
		_, err := f.mgr.Resolve(f.ctx, token)
		if !errors.Is(err, errDB) || IsInvalid(err) {
			t.Fatalf("err = %v, want DB error (bukan ErrInvalid)", err)
		}
	})
	t.Run("session db error", func(t *testing.T) {
		f := newFixture()
		token, _ := f.login(t, aliceID)
		f.repo.err = errDB
		if _, err := f.mgr.Resolve(f.ctx, token); !errors.Is(err, errDB) {
			t.Fatalf("err = %v, want %v", err, errDB)
		}
	})
}

func TestManager_Resolve_TouchThrottled(t *testing.T) {
	f := newFixture()
	token, s := f.login(t, aliceID)

	f.clk.Advance(30 * time.Second)
	if _, err := f.mgr.Resolve(f.ctx, token); err != nil {
		t.Fatal(err)
	}
	if f.repo.touches != 0 {
		t.Fatal("last_seen_at tidak boleh di-update < 1 menit")
	}

	f.clk.Advance(31 * time.Second)
	if _, err := f.mgr.Resolve(f.ctx, token); err != nil {
		t.Fatal(err)
	}
	if f.repo.touches != 1 || !f.repo.rows[s.ID].LastSeenAt.Equal(f.clk.Now()) {
		t.Fatal("last_seen_at harus di-update setelah >= 1 menit")
	}
}

func TestManager_Revoke(t *testing.T) {
	f := newFixture()
	token, s := f.login(t, aliceID)

	if err := f.mgr.Revoke(f.ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mgr.Resolve(f.ctx, token); !IsInvalid(err) {
		t.Fatalf("setelah logout: err = %v, want ErrInvalid", err)
	}
}

func TestManager_Revoke_GraceTokenAlsoDies(t *testing.T) {
	f := newFixture()
	oldToken, s := f.login(t, aliceID)
	f.clk.Advance(15 * time.Minute)
	if _, err := f.mgr.Resolve(f.ctx, oldToken); err != nil {
		t.Fatal(err)
	}

	if err := f.mgr.Revoke(f.ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mgr.Resolve(f.ctx, oldToken); !IsInvalid(err) {
		t.Fatalf("token grace setelah revoke: err = %v, want ErrInvalid", err)
	}
}

func TestManager_RevokeAllForUser(t *testing.T) {
	f := newFixture()
	a1, _ := f.login(t, aliceID)
	a2, _ := f.login(t, aliceID)
	b, _ := f.login(t, bobID)

	if err := f.mgr.RevokeAllForUser(f.ctx, aliceID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{a1, a2} {
		if _, err := f.mgr.Resolve(f.ctx, tok); !IsInvalid(err) {
			t.Fatalf("session alice harus mati: err = %v", err)
		}
	}
	if _, err := f.mgr.Resolve(f.ctx, b); err != nil {
		t.Fatalf("session bob tidak boleh ikut mati: %v", err)
	}
}
