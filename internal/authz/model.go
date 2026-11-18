package authz

import "context"

type Level int

const (
	LevelNone Level = iota
	LevelView
	LevelEdit
)

func (l Level) String() string {
	switch l {
	case LevelView:
		return "view"
	case LevelEdit:
		return "edit"
	default:
		return "none"
	}
}

// ParseLevel mengonversi string database level ('view'/'edit') ke Level.
func ParseLevel(s string) Level {
	switch s {
	case "view":
		return LevelView
	case "edit":
		return LevelEdit
	default:
		return LevelNone
	}
}

// Principal merepresentasikan pengguna yang sedang login.
type Principal struct {
	UserID   int64
	Username string
	IsAdmin  bool
}

type principalKey struct{}

// WithPrincipal menyisipkan Principal ke dalam context (terutama untuk testing handler).
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom mengambil Principal dari context.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// DatabaseAccess merepresentasikan informasi hak akses terhadap satu database.
type DatabaseAccess struct {
	DatabaseID int64
	Name       string
	Level      Level
}

// GrantSource diimplementasikan oleh B (dbconn) dan dikonsumsi oleh A (authz)
// untuk menghitung hak akses efektif pengguna.
type GrantSource interface {
	LevelsForUser(ctx context.Context, userID, dbID int64) ([]Level, error)
	AccessibleDatabases(ctx context.Context, userID int64) ([]DatabaseAccess, error)
}

// Action mendefinisikan aksi terproteksi di policy.go
type Action string

const (
	ActAdminManageUsers       Action = "admin.users.manage"
	ActAdminManageGroups      Action = "admin.groups.manage"
	ActAdminManageConnections Action = "admin.connections.manage"
	ActAdminManageGrants      Action = "admin.grants.manage"
	ActDBView                 Action = "db.view"
	ActMigrationView          Action = "db.migration.view"
	ActMigrationCreate        Action = "db.migration.create"
	ActMigrationEdit          Action = "db.migration.edit"
	ActMigrationExecute       Action = "db.migration.execute"
	ActMigrationDelete        Action = "db.migration.delete"
	ActMigrationExplain       Action = "db.migration.explain"

	// Milestone 2. Aksi alur review/eksekusi; dipakai handler milik C dan A.
	ActMigrationSubmit   Action = "db.migration.submit"   // ajukan review (apply/forward)
	ActMigrationRollback Action = "db.migration.rollback" // ajukan rollback
	ActMigrationRun      Action = "db.migration.run"      // tombol Jalankan: approved → queued
	ActMigrationResume   Action = "db.migration.resume"   // lanjutkan execution partial
	ActMigrationCancel   Action = "db.migration.cancel"   // batalkan sebelum berjalan

	// Approval: cukup login. Pengecekan "leader group" dilakukan service approval, bukan policy.
	ActApprovalView   Action = "approval.view"
	ActApprovalDecide Action = "approval.decide"
)
