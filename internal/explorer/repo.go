package explorer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo interface {
	ListTables(ctx context.Context, pool *pgxpool.Pool) ([]TableSummary, error)
	TableExists(ctx context.Context, pool *pgxpool.Pool, schema, table string) (bool, error)
	GetColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]ColumnInfo, error)
	GetConstraints(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]ConstraintInfo, error)
	GetIndexes(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]IndexInfo, error)
	GetPrimaryKeyColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]string, error)
	GetTableData(ctx context.Context, pool *pgxpool.Pool, schema, table string, page, size int) (*TableDataPage, error)
}

type PgRepo struct{}

func NewPgRepo() *PgRepo {
	return &PgRepo{}
}

// withReadOnlyTx membungkus eksekusi query target DB dalam transaksi READ ONLY dengan timeout 10 detik.
func (r *PgRepo) withReadOnlyTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("explorer: gagal memulai transaksi READ ONLY: %w", err)
	}
	defer tx.Rollback(ctx)

	// Batasi waktu eksekusi query target DB maksimal 10 detik (D14)
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '10s'"); err != nil {
		return fmt.Errorf("explorer: gagal mengatur statement_timeout: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ListTables mengambil seluruh tabel dan view pengguna, mengecualikan skema internal.
func (r *PgRepo) ListTables(ctx context.Context, pool *pgxpool.Pool) ([]TableSummary, error) {
	var tables []TableSummary

	err := r.withReadOnlyTx(ctx, pool, func(tx pgx.Tx) error {
		const q = `
			SELECT
				t.table_schema,
				t.table_name,
				t.table_type,
				COALESCE(c.reltuples::bigint, 0) AS approx_rows
			FROM information_schema.tables t
			LEFT JOIN pg_namespace n ON n.nspname = t.table_schema
			LEFT JOIN pg_class c ON c.relname = t.table_name AND c.relnamespace = n.oid
			WHERE t.table_schema NOT IN ('pg_catalog', 'information_schema', '_dbmp')
			ORDER BY t.table_schema ASC, t.table_name ASC`

		rows, err := tx.Query(ctx, q)
		if err != nil {
			return fmt.Errorf("ListTables query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var ts TableSummary
			if err := rows.Scan(&ts.Schema, &ts.Name, &ts.Type, &ts.ApproxRows); err != nil {
				return fmt.Errorf("ListTables scan: %w", err)
			}
			if ts.ApproxRows < 0 {
				ts.ApproxRows = 0
			}
			tables = append(tables, ts)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}
	return tables, nil
}

// TableExists memeriksa keberadaan tabel pada skema target di katalog PostgreSQL.
func (r *PgRepo) TableExists(ctx context.Context, pool *pgxpool.Pool, schema, table string) (bool, error) {
	var exists bool
	err := r.withReadOnlyTx(ctx, pool, func(tx pgx.Tx) error {
		const q = `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = $1 AND table_name = $2
			)`
		return tx.QueryRow(ctx, q, schema, table).Scan(&exists)
	})
	return exists, err
}

// GetColumns mengambil metadata kolom (nama, tipe data, nullable, default, status PK).
func (r *PgRepo) GetColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]ColumnInfo, error) {
	var cols []ColumnInfo

	err := r.withReadOnlyTx(ctx, pool, func(tx pgx.Tx) error {
		const q = `
			SELECT
				c.column_name,
				c.data_type,
				(c.is_nullable = 'YES') AS is_nullable,
				c.column_default,
				COALESCE((
					SELECT true
					FROM information_schema.table_constraints tc
					JOIN information_schema.key_column_usage kcu
						ON kcu.constraint_name = tc.constraint_name
						AND kcu.table_schema = tc.table_schema
						AND kcu.table_name = tc.table_name
					WHERE tc.constraint_type = 'PRIMARY KEY'
						AND tc.table_schema = c.table_schema
						AND tc.table_name = c.table_name
						AND kcu.column_name = c.column_name
					LIMIT 1
				), false) AS is_pk
			FROM information_schema.columns c
			WHERE c.table_schema = $1 AND c.table_name = $2
			ORDER BY c.ordinal_position ASC`

		rows, err := tx.Query(ctx, q, schema, table)
		if err != nil {
			return fmt.Errorf("GetColumns query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var ci ColumnInfo
			if err := rows.Scan(&ci.Name, &ci.DataType, &ci.IsNullable, &ci.DefaultValue, &ci.IsPrimaryKey); err != nil {
				return fmt.Errorf("GetColumns scan: %w", err)
			}
			cols = append(cols, ci)
		}
		return rows.Err()
	})

	if err != nil {
		return nil, err
	}
	return cols, nil
}

// GetConstraints mengambil constraints tabel (PK, FK, UNIQUE, CHECK).
func (r *PgRepo) GetConstraints(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]ConstraintInfo, error) {
	var constraints []ConstraintInfo

	err := r.withReadOnlyTx(ctx, pool, func(tx pgx.Tx) error {
		const q = `
			SELECT
				con.conname AS constraint_name,
				CASE con.contype
					WHEN 'p' THEN 'PRIMARY KEY'
					WHEN 'f' THEN 'FOREIGN KEY'
					WHEN 'u' THEN 'UNIQUE'
					WHEN 'c' THEN 'CHECK'
					ELSE con.contype::text
				END AS constraint_type,
				pg_get_constraintdef(con.oid, true) AS definition,
				COALESCE(f_cl.relname, '') AS foreign_table,
				COALESCE(f_att.attname, '') AS foreign_column
			FROM pg_constraint con
			JOIN pg_class cl ON cl.oid = con.conrelid
			JOIN pg_namespace ns ON ns.oid = cl.relnamespace
			LEFT JOIN pg_class f_cl ON f_cl.oid = con.confrelid
			LEFT JOIN pg_attribute f_att ON f_att.attrelid = con.confrelid
				AND f_att.attnum = con.confkey[1]
			WHERE ns.nspname = $1 AND cl.relname = $2
			ORDER BY con.conname ASC`

		rows, err := tx.Query(ctx, q, schema, table)
		if err != nil {
			return fmt.Errorf("GetConstraints query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var c ConstraintInfo
			if err := rows.Scan(&c.Name, &c.Type, &c.Definition, &c.ForeignTable, &c.ForeignColumn); err != nil {
				return fmt.Errorf("GetConstraints scan: %w", err)
			}
			constraints = append(constraints, c)
		}
		return rows.Err()
	})

	if err != nil {
		return nil, err
	}
	return constraints, nil
}

// GetIndexes mengambil informasi indeks pada tabel dari pg_indexes.
func (r *PgRepo) GetIndexes(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]IndexInfo, error) {
	var indexes []IndexInfo

	err := r.withReadOnlyTx(ctx, pool, func(tx pgx.Tx) error {
		const q = `
			SELECT
				indexname,
				indexdef,
				(indexdef LIKE 'CREATE UNIQUE INDEX%') AS is_unique,
				(indexname LIKE '%_pkey' OR indexdef LIKE '%PRIMARY KEY%') AS is_primary
			FROM pg_indexes
			WHERE schemaname = $1 AND tablename = $2
			ORDER BY indexname ASC`

		rows, err := tx.Query(ctx, q, schema, table)
		if err != nil {
			return fmt.Errorf("GetIndexes query: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var idx IndexInfo
			if err := rows.Scan(&idx.Name, &idx.Definition, &idx.IsUnique, &idx.IsPrimary); err != nil {
				return fmt.Errorf("GetIndexes scan: %w", err)
			}
			indexes = append(indexes, idx)
		}
		return rows.Err()
	})

	if err != nil {
		return nil, err
	}
	return indexes, nil
}

// GetPrimaryKeyColumns mengambil daftar nama kolom yang menjadi Primary Key tabel.
func (r *PgRepo) GetPrimaryKeyColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]string, error) {
	var pks []string

	err := r.withReadOnlyTx(ctx, pool, func(tx pgx.Tx) error {
		const q = `
			SELECT kcu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
				ON kcu.constraint_name = tc.constraint_name
				AND kcu.table_schema = tc.table_schema
				AND kcu.table_name = tc.table_name
			WHERE tc.constraint_type = 'PRIMARY KEY'
				AND tc.table_schema = $1 AND tc.table_name = $2
			ORDER BY kcu.ordinal_position ASC`

		rows, err := tx.Query(ctx, q, schema, table)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err != nil {
				return err
			}
			pks = append(pks, col)
		}
		return rows.Err()
	})

	if err != nil {
		return nil, err
	}
	return pks, nil
}

// GetTableData mengambil baris data dari tabel dengan paginasi dan pengurutan Primary Key.
// Nama tabel dan skema di-sanitize menggunakan pgx.Identifier untuk mencegah SQL Injection.
func (r *PgRepo) GetTableData(ctx context.Context, pool *pgxpool.Pool, schema, table string, page, size int) (*TableDataPage, error) {
	sanitizedTable := pgx.Identifier{schema, table}.Sanitize()

	pks, err := r.GetPrimaryKeyColumns(ctx, pool, schema, table)
	if err != nil {
		return nil, err
	}

	var dataPage TableDataPage
	dataPage.Schema = schema
	dataPage.Table = table
	dataPage.Page = page
	dataPage.Size = size
	dataPage.PKColumns = pks

	err = r.withReadOnlyTx(ctx, pool, func(tx pgx.Tx) error {
		// 1. Hitung total baris
		countQuery := fmt.Sprintf("SELECT count(*) FROM %s", sanitizedTable)
		if err := tx.QueryRow(ctx, countQuery).Scan(&dataPage.TotalRows); err != nil {
			return fmt.Errorf("GetTableData count query: %w", err)
		}

		// Hitung pagination
		if dataPage.TotalRows > 0 {
			dataPage.TotalPages = int((dataPage.TotalRows + int64(size) - 1) / int64(size))
		} else {
			dataPage.TotalPages = 1
		}
		dataPage.HasNext = page < dataPage.TotalPages
		dataPage.HasPrev = page > 1
		dataPage.PrevPage = page - 1
		dataPage.NextPage = page + 1

		// 2. Susun klausa ORDER BY
		orderClause := ""
		if len(pks) > 0 {
			var quotedPKs []string
			for _, pk := range pks {
				quotedPKs = append(quotedPKs, pgx.Identifier{pk}.Sanitize())
			}
			orderClause = "ORDER BY " + strings.Join(quotedPKs, ", ")
		}

		// 3. Query baris data
		offset := (page - 1) * size
		dataQuery := fmt.Sprintf("SELECT * FROM %s %s LIMIT $1 OFFSET $2", sanitizedTable, orderClause)

		rows, err := tx.Query(ctx, dataQuery, size, offset)
		if err != nil {
			return fmt.Errorf("GetTableData query: %w", err)
		}
		defer rows.Close()

		// Baca nama kolom dari metadata response
		fieldDescs := rows.FieldDescriptions()
		for _, fd := range fieldDescs {
			dataPage.Columns = append(dataPage.Columns, fd.Name)
		}

		// Scan setiap baris
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				return fmt.Errorf("GetTableData scan values: %w", err)
			}

			var strRow []string
			for _, v := range vals {
				strRow = append(strRow, formatValue(v))
			}
			dataPage.Rows = append(dataPage.Rows, strRow)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}
	return &dataPage, nil
}

// formatValue mengubah nilai database sembarang ke bentuk teks yang representatif untuk viewer.
func formatValue(val any) string {
	if val == nil {
		return "NULL"
	}
	switch v := val.(type) {
	case time.Time:
		return v.Format("2006-01-02 15:04:05 MST")
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
