package migration

import (
	"errors"
	"strings"
	"unicode"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

const maxSQLBytes = 256 * 1024

// Input adalah isi form buat/ubah migration.
type Input struct {
	Title              string
	UpSQL              string
	DownSQL            string
	Irreversible       bool
	IrreversibleReason string
	ExecMode           string
	BatchTable         string
	BatchColumn        string
	BatchSize          int
}

// Normalized mengisi mode default dan memotong spasi. Tidak mengubah penerima.
func (in Input) Normalized() Input {
	in.Title = strings.TrimSpace(in.Title)
	in.UpSQL = strings.TrimSpace(in.UpSQL)
	in.DownSQL = strings.TrimSpace(in.DownSQL)
	in.IrreversibleReason = strings.TrimSpace(in.IrreversibleReason)
	in.ExecMode = strings.TrimSpace(in.ExecMode)
	in.BatchTable = strings.TrimSpace(in.BatchTable)
	in.BatchColumn = strings.TrimSpace(in.BatchColumn)
	if in.ExecMode == "" {
		in.ExecMode = ModeNormal
	}
	if in.Irreversible {
		in.DownSQL = ""
	}
	if in.ExecMode != ModeBatch {
		in.BatchTable = ""
		in.BatchColumn = ""
		in.BatchSize = 0
	}
	return in
}

// Validate memeriksa judul, SQL up/down, alasan irreversible, dan konfigurasi batch.
func (in Input) Validate() error {
	in = in.Normalized()
	fields := map[string]string{}
	if in.Title == "" {
		fields["title"] = "Judul wajib diisi"
	}
	if in.ExecMode != ModeNormal && in.ExecMode != ModeBatch {
		fields["exec_mode"] = "Mode harus normal atau batch"
	}
	if msg := sqlProblem(in.UpSQL); msg != "" {
		fields["up_sql"] = msg
	}
	if in.Irreversible {
		if in.IrreversibleReason == "" {
			fields["irreversible_reason"] = "Alasan wajib diisi untuk migration yang tidak dapat di-rollback"
		}
	} else if msg := sqlProblem(in.DownSQL); msg != "" {
		fields["down_sql"] = msg
	}
	if in.ExecMode == ModeBatch {
		if !validIdent(in.BatchTable) {
			fields["batch_table"] = "Nama tabel batch tidak valid"
		}
		if !validIdent(in.BatchColumn) {
			fields["batch_column"] = "Nama kolom batch tidak valid"
		}
		if in.BatchSize <= 0 {
			fields["batch_size"] = "Ukuran batch harus lebih dari 0"
		}
		if fields["up_sql"] == "" && !hasBatchRange(in.UpSQL) {
			fields["up_sql"] = "SQL batch wajib memuat :from dan :to"
		}
		if !in.Irreversible && fields["down_sql"] == "" && !hasBatchRange(in.DownSQL) {
			fields["down_sql"] = "SQL batch wajib memuat :from dan :to"
		}
	}
	if len(fields) > 0 {
		return apperr.InvalidFields("Input tidak valid", fields)
	}
	return nil
}

func hasBatchRange(sql string) bool {
	return strings.Contains(sql, ":from") && strings.Contains(sql, ":to")
}

func validIdent(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if i > 0 && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func sqlProblem(sqlText string) string {
	err := validateSQL(sqlText)
	if err == nil {
		return ""
	}
	if fe, ok := apperr.FieldErrors(err); ok {
		if msg := fe["sql_text"]; msg != "" {
			return msg
		}
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Msg != "" {
		return appErr.Msg
	}
	return "SQL tidak valid"
}

func validateSQL(sqlText string) error {
	if strings.TrimSpace(sqlText) == "" {
		return apperr.InvalidFields("Input tidak valid", map[string]string{
			"sql_text": "SQL wajib diisi",
		})
	}
	if len(sqlText) > maxSQLBytes {
		return apperr.InvalidFields("Input tidak valid", map[string]string{
			"sql_text": "SQL maksimal 256 KB",
		})
	}
	stmts, err := SplitStatements(sqlText)
	if err != nil {
		return apperr.InvalidFields("Input tidak valid", map[string]string{
			"sql_text": "SQL tidak valid: kutipan atau komentar tidak ditutup",
		})
	}
	if len(stmts) == 0 {
		return apperr.InvalidFields("Input tidak valid", map[string]string{
			"sql_text": "SQL wajib berisi minimal satu statement",
		})
	}
	for _, st := range stmts {
		if st.Kind == KindTxControl {
			return ErrRejectedSQL
		}
		tokens, err := lexKeywords(st.SQL)
		if err != nil {
			return apperr.InvalidFields("Input tidak valid", map[string]string{
				"sql_text": "SQL tidak valid: kutipan atau komentar tidak ditutup",
			})
		}
		if unsafeTokens(tokens) {
			return ErrRejectedSQL
		}
	}
	return nil
}

func unsafeTokens(tokens []string) bool {
	if hasCreateDatabase(tokens) || hasCreateIndexConcurrently(tokens) {
		return true
	}
	for _, tok := range tokens {
		if tok == "VACUUM" {
			return true
		}
	}
	return false
}

func hasCreateDatabase(tokens []string) bool {
	for i := 0; i < len(tokens)-1; i++ {
		if tokens[i] == "CREATE" && tokens[i+1] == "DATABASE" {
			return true
		}
	}
	return false
}

func hasCreateIndexConcurrently(tokens []string) bool {
	seenCreate := false
	seenIndex := false
	for _, tok := range tokens {
		switch tok {
		case "CREATE":
			if !seenIndex {
				seenCreate = true
			}
		case "INDEX":
			if seenCreate {
				seenIndex = true
			}
		case "CONCURRENTLY":
			if seenIndex {
				return true
			}
		}
	}
	return false
}

// ValidateSQL applies the same rules as saving a migration (D13). Package explain uses it
// before running statements against a target database.
func ValidateSQL(sqlText string) error {
	return validateSQL(sqlText)
}
