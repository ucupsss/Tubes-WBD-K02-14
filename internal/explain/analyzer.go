package explain

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/dbconn"
	"github.com/ucupsss/Tubes-WBD-K02-14/internal/migration"
)

// Analyzer menjalankan EXPLAIN terhadap script migration di database target tanpa meninggalkan perubahan.
type Analyzer struct {
	conns dbconn.Manager
}

func NewAnalyzer(conns dbconn.Manager) *Analyzer {
	return &Analyzer{conns: conns}
}

// Analyze memecah script menjadi statement lalu, dalam SATU transaction yang selalu di-rollback:
//   - statement DML (SELECT/INSERT/UPDATE/DELETE/MERGE/WITH/VALUES) di-EXPLAIN (FORMAT JSON),
//     dengan ANALYZE + BUFFERS bila analyze=true (statement benar-benar dieksekusi lalu di-rollback);
//   - statement lain (DDL) tidak punya execution plan; statement itu dijalankan sementara supaya
//     statement berikutnya (mis. INSERT ke table yang baru dibuat) bisa direncanakan.
//
// Setiap statement dibungkus SAVEPOINT, sehingga error satu statement dilaporkan per statement
// tanpa menghentikan analisis statement lain.
func (a *Analyzer) Analyze(ctx context.Context, dbID int64, sqlText string, analyze bool) (Report, error) {
	if err := migration.ValidateSQL(sqlText); err != nil {
		return Report{}, err
	}
	stmts, err := migration.SplitStatements(sqlText)
	if err != nil {
		return Report{}, apperr.Invalid("SQL tidak valid")
	}

	pool, err := a.conns.Pool(ctx, dbID)
	if errors.Is(err, dbconn.ErrNotFound) {
		return Report{}, apperr.NotFound("Database tidak ditemukan")
	}
	if err != nil {
		return Report{}, apperr.Unavailable("Database target tidak dapat dihubungi", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Report{}, apperr.Unavailable("Database target tidak dapat dihubungi", err)
	}
	// Apa pun hasilnya, tidak ada perubahan yang boleh tersisa di target.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	for _, q := range []string{`SET LOCAL statement_timeout = '30s'`, `SET LOCAL lock_timeout = '5s'`} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return Report{}, fmt.Errorf("explain: %s: %w", q, err)
		}
	}

	report := Report{Analyze: analyze}
	for _, st := range stmts {
		res := StatementResult{Index: st.Index + 1, SQL: st.SQL, Kind: string(st.Kind)}
		if err := runStatement(ctx, tx, st, analyze, &res); err != nil {
			return Report{}, err
		}
		if res.Error != "" {
			report.Failed++
		}
		report.Statements = append(report.Statements, res)
	}
	return report, nil
}

// runStatement mengisi res. Error yang dikembalikan hanya error koneksi/transaction, bukan error SQL user.
func runStatement(ctx context.Context, tx pgx.Tx, st migration.Statement, analyze bool, res *StatementResult) error {
	if _, err := tx.Exec(ctx, "SAVEPOINT dbmp_explain"); err != nil {
		return fmt.Errorf("explain: savepoint: %w", err)
	}

	var stmtErr error
	if st.Kind == migration.KindDML {
		opts := "FORMAT JSON"
		if analyze {
			opts = "ANALYZE, BUFFERS, FORMAT JSON"
		}
		var results []*pgconn.Result
		results, stmtErr = execRaw(ctx, tx, "EXPLAIN ("+opts+") "+st.SQL)
		if stmtErr == nil {
			if len(results) == 0 || len(results[0].Rows) == 0 || len(results[0].Rows[0]) == 0 {
				return fmt.Errorf("explain: hasil EXPLAIN kosong")
			}
			plan, err := ParsePlan(results[0].Rows[0][0])
			if err != nil {
				return err
			}
			res.Plan = &plan
		}
	} else {
		_, stmtErr = execRaw(ctx, tx, st.SQL)
		if stmtErr == nil {
			res.Note = "Statement ini tidak memiliki execution plan (DDL). Statement dijalankan sementara agar statement berikutnya dapat dianalisis, lalu di-rollback."
		}
	}

	if stmtErr != nil {
		res.Error = safeError(stmtErr)
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT dbmp_explain"); err != nil {
			return fmt.Errorf("explain: rollback to savepoint: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT dbmp_explain"); err != nil {
		return fmt.Errorf("explain: release savepoint: %w", err)
	}
	return nil
}

// execRaw mengirim teks SQL apa adanya lewat simple query protocol, tanpa pemrosesan placeholder
// oleh driver, sehingga literal berisi `$1` atau dollar-quote tetap utuh.
func execRaw(ctx context.Context, tx pgx.Tx, sql string) ([]*pgconn.Result, error) {
	return tx.Conn().PgConn().Exec(ctx, sql).ReadAll()
}

// safeError hanya meneruskan pesan error Postgres (mis. "relation ... does not exist").
func safeError(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Message != "" {
		return pgErr.Message
	}
	return "Statement gagal dianalisis"
}
