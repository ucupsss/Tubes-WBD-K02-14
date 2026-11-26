package approval

import (
	"context"
	"fmt"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/platformdb"
)

// Repo mengakses tabel execution_approvals. Semua method menerima DBTX supaya bisa dipakai di dalam transaksi.
type Repo interface {
	// OpenRound membuat baris pending untuk setiap group yang punya akses ke dbID; mengembalikan jumlah baris.
	OpenRound(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) (int, error)
	// MaxRound mengembalikan ronde terbesar untuk execution (0 bila belum ada).
	MaxRound(ctx context.Context, q platformdb.DBTX, execID int64) (int, error)
	// Decide mengisi keputusan untuk semua baris pending di ronde ini milik group yang dipimpin userID.
	Decide(ctx context.Context, q platformdb.DBTX, execID int64, round int, userID int64, d Decision, comment string) (int, error)
	// LeadsRow melaporkan apakah userID memimpin group yang punya baris (apa pun statusnya) di ronde ini.
	LeadsRow(ctx context.Context, q platformdb.DBTX, execID int64, round int, userID int64) (bool, error)
	// Counts menghitung baris pending dan rejected di ronde ini.
	Counts(ctx context.Context, q platformdb.DBTX, execID int64, round int) (pending, rejected int, err error)
	// Missing mengembalikan group yang punya akses sekarang tetapi belum punya approval sah di ronde ini.
	Missing(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) ([]int64, error)
	// GrantGroupCount mengembalikan jumlah group yang saat ini memiliki akses ke dbID.
	GrantGroupCount(ctx context.Context, q platformdb.DBTX, dbID int64) (int, error)
	// ListByExecution mengembalikan semua baris approval (semua ronde) untuk halaman detail.
	ListByExecution(ctx context.Context, q platformdb.DBTX, execID int64) ([]Approval, error)
	// PendingForLeader mengembalikan antrean approval milik leader (hanya ronde terbaru tiap execution).
	PendingForLeader(ctx context.Context, q platformdb.DBTX, userID int64) ([]PendingItem, error)
}

// PgRepo adalah implementasi PostgreSQL.
type PgRepo struct{}

func NewPgRepo() *PgRepo { return &PgRepo{} }

func (r *PgRepo) OpenRound(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) (int, error) {
	const sql = `
		INSERT INTO execution_approvals (execution_id, round, group_id, leader_user_id)
		SELECT $1, $2, g.id, g.leader_id
		FROM group_db_access gda
		JOIN groups g ON g.id = gda.group_id
		WHERE gda.database_id = $3
		ON CONFLICT DO NOTHING`
	tag, err := q.Exec(ctx, sql, execID, round, dbID)
	if err != nil {
		return 0, fmt.Errorf("approval.OpenRound: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *PgRepo) MaxRound(ctx context.Context, q platformdb.DBTX, execID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT COALESCE(max(round), 0) FROM execution_approvals WHERE execution_id = $1`, execID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("approval.MaxRound: %w", err)
	}
	return n, nil
}

func (r *PgRepo) Decide(ctx context.Context, q platformdb.DBTX, execID int64, round int, userID int64, d Decision, comment string) (int, error) {
	const sql = `
		UPDATE execution_approvals a
		SET decision = $4, comment = NULLIF($5, ''), decided_by = $3, leader_user_id = $3, decided_at = now()
		FROM groups g
		WHERE a.group_id = g.id
		  AND a.execution_id = $1 AND a.round = $2
		  AND a.decision = 'pending'
		  AND g.leader_id = $3`
	tag, err := q.Exec(ctx, sql, execID, round, userID, string(d), comment)
	if err != nil {
		return 0, fmt.Errorf("approval.Decide: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *PgRepo) LeadsRow(ctx context.Context, q platformdb.DBTX, execID int64, round int, userID int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM execution_approvals a JOIN groups g ON g.id = a.group_id
			WHERE a.execution_id = $1 AND a.round = $2 AND g.leader_id = $3)`,
		execID, round, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("approval.LeadsRow: %w", err)
	}
	return ok, nil
}

func (r *PgRepo) Counts(ctx context.Context, q platformdb.DBTX, execID int64, round int) (int, int, error) {
	var pending, rejected int
	err := q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE decision = 'pending'),
		       count(*) FILTER (WHERE decision = 'rejected')
		FROM execution_approvals WHERE execution_id = $1 AND round = $2`,
		execID, round).Scan(&pending, &rejected)
	if err != nil {
		return 0, 0, fmt.Errorf("approval.Counts: %w", err)
	}
	return pending, rejected, nil
}

func (r *PgRepo) Missing(ctx context.Context, q platformdb.DBTX, execID, dbID int64, round int) ([]int64, error) {
	const sql = `
		SELECT g.id
		FROM group_db_access gda
		JOIN groups g ON g.id = gda.group_id
		WHERE gda.database_id = $1
		  AND NOT EXISTS (
			SELECT 1 FROM execution_approvals a
			WHERE a.execution_id = $2 AND a.round = $3 AND a.group_id = g.id
			  AND a.decision = 'approved'
			  AND g.leader_id IS NOT NULL
			  AND a.leader_user_id = g.leader_id)
		ORDER BY g.id`
	rows, err := q.Query(ctx, sql, dbID, execID, round)
	if err != nil {
		return nil, fmt.Errorf("approval.Missing: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("approval.Missing scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *PgRepo) GrantGroupCount(ctx context.Context, q platformdb.DBTX, dbID int64) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM group_db_access WHERE database_id = $1`, dbID).Scan(&n); err != nil {
		return 0, fmt.Errorf("approval.GrantGroupCount: %w", err)
	}
	return n, nil
}

func (r *PgRepo) ListByExecution(ctx context.Context, q platformdb.DBTX, execID int64) ([]Approval, error) {
	const sql = `
		SELECT a.execution_id, a.round, a.group_id, g.name, g.leader_id,
		       COALESCE(lu.username, ''), a.decision, COALESCE(a.comment, ''),
		       COALESCE(du.username, ''), a.decided_at
		FROM execution_approvals a
		JOIN groups g ON g.id = a.group_id
		LEFT JOIN users lu ON lu.id = g.leader_id
		LEFT JOIN users du ON du.id = a.decided_by
		WHERE a.execution_id = $1
		ORDER BY a.round, g.name`
	rows, err := q.Query(ctx, sql, execID)
	if err != nil {
		return nil, fmt.Errorf("approval.ListByExecution: %w", err)
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		var a Approval
		var d string
		if err := rows.Scan(&a.ExecutionID, &a.Round, &a.GroupID, &a.GroupName, &a.LeaderID,
			&a.LeaderName, &d, &a.Comment, &a.DecidedByName, &a.DecidedAt); err != nil {
			return nil, fmt.Errorf("approval.ListByExecution scan: %w", err)
		}
		a.Decision = Decision(d)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *PgRepo) PendingForLeader(ctx context.Context, q platformdb.DBTX, userID int64) ([]PendingItem, error) {
	const sql = `
		SELECT a.execution_id, a.round, a.group_id, g.name, a.created_at
		FROM execution_approvals a
		JOIN groups g ON g.id = a.group_id
		JOIN executions e ON e.id = a.execution_id AND e.status = 'pending_review'
		WHERE g.leader_id = $1
		  AND a.decision = 'pending'
		  AND a.round = (SELECT max(x.round) FROM execution_approvals x WHERE x.execution_id = a.execution_id)
		ORDER BY a.created_at, a.execution_id`
	rows, err := q.Query(ctx, sql, userID)
	if err != nil {
		return nil, fmt.Errorf("approval.PendingForLeader: %w", err)
	}
	defer rows.Close()
	var out []PendingItem
	for rows.Next() {
		var p PendingItem
		if err := rows.Scan(&p.ExecutionID, &p.Round, &p.GroupID, &p.GroupName, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("approval.PendingForLeader scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
