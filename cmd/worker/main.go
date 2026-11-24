package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ucupsss/Tubes-WBD-K02-14/db"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/admin/databases"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/approval"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/config"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/lock"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/queue"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/worker"
)

func main() {
	cfg := config.MustLoad()
	logger := logx.New(os.Stdout, slog.LevelInfo)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	workerID := cfg.WorkerID
	if workerID == "" {
		workerID, err = os.Hostname()
		if err != nil || workerID == "" {
			workerID = "worker"
		}
	}
	repo := migration.NewPgRepo().Bind(pool)
	conns := dbconn.NewManager(connGetter{db: pool, repo: databases.NewPgRepo()}, cfg.SecretKey)
	defer conns.CloseAll()
	approvals := approval.NewService(pool, approval.PoolTx{Pool: pool}, approval.NewPgRepo(), repo)
	runner := migration.NewExecutor(pool, repo, conns, lock.New(pool), migration.WrapApproval(approvals), authz.NewAuthorizer(dbconn.NewGrantRepo(pool)))
	logger.Info("worker started", "worker_id", workerID, "concurrency", cfg.WorkerConcurrency)
	err = worker.Run(ctx, queue.New(pool), repo, runner, worker.Config{
		WorkerID:    workerID,
		Concurrency: cfg.WorkerConcurrency,
		Poll:        cfg.WorkerPollInterval,
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("worker stopped", "err", err)
		os.Exit(1)
	}
}
