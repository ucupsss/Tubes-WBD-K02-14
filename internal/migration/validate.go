package migration

import (
	"errors"
	"strings"

	"github.com/ucupsss/Tubes-WBD-K02-14/internal/apperr"
)

const maxSQLBytes = 256 * 1024

// CreateInput is the editor payload for a new or updated migration.
type CreateInput struct {
	DatabaseID int64
	Title      string
	SQLText    string
}

// Validate checks the title and SQL. Rejected SQL is reported on the sql_text field.
func (in CreateInput) Validate() error {
	fields := map[string]string{}
	if strings.TrimSpace(in.Title) == "" {
		fields["title"] = "Judul wajib diisi"
	}
	if msg := sqlProblem(in.SQLText); msg != "" {
		fields["sql_text"] = msg
	}
	if len(fields) > 0 {
		return apperr.InvalidFields("Input tidak valid", fields)
	}
	return nil
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
