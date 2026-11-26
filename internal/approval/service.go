package approval

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/authz"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/logx"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// Executions diimplementasi C (package migration): membaca dan mengubah status execution.
// Approval tidak menyentuh tabel executions secara langsung.
type Executions interface {
	// Get mengembalikan database, ronde approval, dan status execution. NotFound bila tidak ada.
	Get(ctx context.Context, q platformdb.DBTX, execID int64) (ExecInfo, error)
	// SetStatus mengubah status execution (approved, rejected, pending_review) di dalam transaksi pemanggil.
	SetStatus(ctx context.Context, q platformdb.DBTX, execID int64, status string) error
	// SetRound mengubah approval_round execution (dipakai Revoke).
	SetRound(ctx context.Context, q platformdb.DBTX, execID int64, round int) error
}

// UnavailableExecutions dipakai selama tabel/servis execution milik C belum ada.
type UnavailableExecutions struct{}

func (UnavailableExecutions) Get(context.Context, platformdb.DBTX, int64) (ExecInfo, error) {
	return ExecInfo{}, ErrExecUnavailable
}
func (UnavailableExecutions) SetStatus(context.Context, platformdb.DBTX, int64, string) error {
	return ErrExecUnavailable
}
func (UnavailableExecutions) SetRound(context.Context, platformdb.DBTX, int64, int) error {
	return ErrExecUnavailable
}

// TxRunner menjalankan fn dalam satu transaksi (commit bila nil, rollback bila error).
type TxRunner interface {
	InTx(ctx context.Context, fn func(q platformdb.DBTX) error) error
}

// PoolTx adalah TxRunner untuk pgxpool.
type PoolTx struct{ Pool *pgxpool.Pool }

func (p PoolTx) InTx(ctx context.Context, fn func(q platformdb.DBTX) error) error {
	return platformdb.WithTx(ctx, p.Pool, func(tx pgx.Tx) error { return fn(tx) })
}

// Service adalah logika approval. Juga memenuhi interface Checker yang dipakai C (OpenRound, Revoke, Verify).
type Service struct {
	db    platformdb.DBTX
	tx    TxRunner
	repo  Repo
	execs Executions
}

func NewService(db platformdb.DBTX, tx TxRunner, repo Repo, execs Executions) *Service {
	if execs == nil {
		execs = UnavailableExecutions{}
	}
	return &Service{db: db, tx: tx, repo: repo, execs: execs}
}

// OpenRound membuat baris approval pending untuk setiap group dengan akses ke dbID. Dipanggil C di dalam
// transaksi submit/forward/rollback. Group tanpa leader tetap dibuatkan baris; execution menunggu leader ditetapkan.
func (s *Service) OpenRound(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) error {
	n, err := s.repo.OpenRound(ctx, q, execID, dbID, round)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNoGroups
	}
	return nil
}

// Revoke mencabut approval karena akses berubah sebelum job berjalan (D40): ronde baru dibuka dan execution
// kembali ke pending_review. Mengembalikan nomor ronde baru. Dipanggil C dari worker saat revalidasi.
func (s *Service) Revoke(ctx context.Context, q platformdb.DBTX, execID, dbID int64) (int, error) {
	cur, err := s.repo.MaxRound(ctx, q, execID)
	if err != nil {
		return 0, err
	}
	next := cur + 1
	if err := s.OpenRound(ctx, q, execID, dbID, next); err != nil {
		return 0, err
	}
	if err := s.execs.SetRound(ctx, q, execID, next); err != nil {
		return 0, err
	}
	if err := s.execs.SetStatus(ctx, q, execID, StatusPendingReview); err != nil {
		return 0, err
	}
	logx.Audit(ctx, "approval.revoked",
		slog.Int64("execution_id", execID), slog.Int("round", next))
	return next, nil
}

// Verify dipakai worker saat revalidasi: approval dianggap lengkap bila SETIAP group yang punya akses
// saat ini punya approval 'approved' dari leader group tersebut saat ini, dan tidak ada yang rejected.
func (s *Service) Verify(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) (Verification, error) {
	n, err := s.repo.GrantGroupCount(ctx, q, dbID)
	if err != nil {
		return Verification{}, err
	}
	if n == 0 {
		return Verification{Complete: false}, nil
	}
	missing, err := s.repo.Missing(ctx, q, execID, dbID, round)
	if err != nil {
		return Verification{}, err
	}
	_, rejected, err := s.repo.Counts(ctx, q, execID, round)
	if err != nil {
		return Verification{}, err
	}
	return Verification{Complete: len(missing) == 0 && rejected == 0, MissingGroupIDs: missing}, nil
}

// Decide mencatat keputusan seorang leader. Satu keputusan berlaku untuk semua group yang ia pimpin dan
// masih pending di execution ini. Reject oleh satu leader menolak seluruh execution; semua approved → approved.
// Requester yang juga leader boleh meng-approve sendiri (D28).
func (s *Service) Decide(ctx context.Context, p authz.Principal, execID int64, approve bool, comment string) (Outcome, error) {
	decision := Rejected
	if approve {
		decision = Approved
	}
	var out Outcome
	err := s.tx.InTx(ctx, func(q platformdb.DBTX) error {
		info, err := s.execs.Get(ctx, q, execID)
		if err != nil {
			return err
		}
		if info.Status != StatusPendingReview {
			return ErrNotPending
		}
		n, err := s.repo.Decide(ctx, q, execID, info.Round, p.UserID, decision, comment)
		if err != nil {
			return err
		}
		if n == 0 {
			leads, err := s.repo.LeadsRow(ctx, q, execID, info.Round, p.UserID)
			if err != nil {
				return err
			}
			if leads {
				return ErrAlreadyDecided
			}
			return ErrNotLeader
		}

		if decision == Rejected {
			out.Status = StatusRejected
			return s.execs.SetStatus(ctx, q, execID, StatusRejected)
		}
		pending, rejected, err := s.repo.Counts(ctx, q, execID, info.Round)
		if err != nil {
			return err
		}
		if pending == 0 && rejected == 0 {
			out.Status = StatusApproved
			return s.execs.SetStatus(ctx, q, execID, StatusApproved)
		}
		out.Status = StatusPendingReview
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}
	action := "reject"
	if approve {
		action = "approve"
	}
	logx.Audit(logx.WithActor(ctx, p.Username), action,
		slog.Int64("execution_id", execID), slog.String("status", out.Status))
	return out, nil
}

// Pending adalah antrean approval milik leader (inbox).
func (s *Service) Pending(ctx context.Context, userID int64) ([]PendingItem, error) {
	return s.repo.PendingForLeader(ctx, s.db, userID)
}

// ListByExecution dipakai halaman detail execution.
func (s *Service) ListByExecution(ctx context.Context, execID int64) ([]Approval, error) {
	return s.repo.ListByExecution(ctx, s.db, execID)
}
