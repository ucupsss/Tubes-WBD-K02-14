package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/db"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/access"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/auth"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

func main() {
	cfg := config.MustLoad()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
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

	alice := ensureUser(ctx, pool, "alice", "password123")
	bob := ensureUser(ctx, pool, "bob", "password123")
	carol := ensureUser(ctx, pool, "carol", "password123")

	dev := ensureGroup(ctx, pool, "dev", "Tim pengembang")
	analyst := ensureGroup(ctx, pool, "analyst", "Tim analis")
	ensureMember(ctx, pool, dev, alice)
	ensureMember(ctx, pool, dev, bob)
	ensureMember(ctx, pool, analyst, bob)
	ensureMember(ctx, pool, analyst, carol)

	createdBy := alice
	if admin, err := auth.NewPgRepo().GetByUsername(ctx, pool, cfg.BootstrapAdminUsername); err == nil {
		createdBy = admin.ID
	}

	db1 := ensureConnection(ctx, pool, cfg.SecretKey, createdBy, databases.CreateInput{
		Name: "target-db-1", Host: "localhost", Port: 5433, DBName: "target1",
		Username: "target", Password: "target", SSLMode: "disable", CreatedBy: createdBy,
	})
	db2 := ensureConnection(ctx, pool, cfg.SecretKey, createdBy, databases.CreateInput{
		Name: "target-db-2", Host: "localhost", Port: 5434, DBName: "target2",
		Username: "target", Password: "target", SSLMode: "disable", CreatedBy: createdBy,
	})

	grants := access.NewService(pool, access.NewPgRepo(), nil)
	must(grants.SaveAccess(ctx, access.SaveAccessInput{
		GroupID: dev, GrantedBy: createdBy,
		Grants: map[int64]string{db1: "edit"},
	}))
	must(grants.SaveAccess(ctx, access.SaveAccessInput{
		GroupID: analyst, GrantedBy: createdBy,
		Grants: map[int64]string{db1: "view", db2: "view"},
	}))

	logger.Info("seed complete",
		"alice", alice, "bob", bob, "carol", carol,
		"dev", dev, "analyst", analyst,
		"target_db_1", db1, "target_db_2", db2,
	)
}

func ensureUser(ctx context.Context, pool *pgxpool.Pool, username, password string) int64 {
	repo := auth.NewPgRepo()
	u, err := repo.GetByUsername(ctx, pool, username)
	if err == nil {
		return u.ID
	}
	if !errors.Is(err, auth.ErrNotFound) {
		fatal(err)
	}
	id, err := auth.NewService(pool, repo).CreateUser(ctx, auth.CreateUserInput{
		Username: username,
		Password: password,
	})
	if err != nil {
		fatal(err)
	}
	return id
}

func ensureGroup(ctx context.Context, pool *pgxpool.Pool, name, description string) int64 {
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, description)
		VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET description = EXCLUDED.description
		RETURNING id`, name, description).Scan(&id)
	if err != nil {
		fatal(err)
	}
	return id
}

func ensureMember(ctx context.Context, pool *pgxpool.Pool, groupID, userID int64) {
	_, err := pool.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, groupID, userID)
	if err != nil {
		fatal(err)
	}
}

func ensureConnection(ctx context.Context, pool *pgxpool.Pool, secret []byte, createdBy int64, in databases.CreateInput) int64 {
	svc := databases.NewService(pool, databases.NewPgRepo(), nil, secret)
	list, err := svc.List(ctx)
	if err != nil {
		fatal(err)
	}
	for _, c := range list {
		if c.Name == in.Name {
			return c.ID
		}
	}
	in.CreatedBy = createdBy
	id, err := svc.Create(ctx, in)
	if err != nil {
		fatal(err)
	}
	return id
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	slog.Error("seed failed", "err", err)
	os.Exit(1)
}
