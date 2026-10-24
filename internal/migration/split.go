package migration

import (
	"strings"
	"unicode"
)

// StatementKind classifies one SQL statement for the executor and EXPLAIN.
type StatementKind string

const (
	KindDML       StatementKind = "dml"
	KindDDL       StatementKind = "ddl"
	KindTxControl StatementKind = "tx_control"
	KindOther     StatementKind = "other"
)

// Statement is one statement with the trailing semicolon removed.
type Statement struct {
	Index int
	SQL   string
	Kind  StatementKind
}

// SplitStatements splits sql on semicolons that are not inside quotes, dollar quotes, or comments.
// The final statement may omit the semicolon. Empty input returns no statements.
func SplitStatements(sql string) ([]Statement, error) {
	parts, err := splitRaw(sql)
	if err != nil {
		return nil, err
	}
	out := make([]Statement, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tokens, err := lexKeywords(part)
		if err != nil {
			return nil, err
		}
		if len(tokens) == 0 {
			continue
		}
		out = append(out, Statement{
			Index: len(out),
			SQL:   part,
			Kind:  kindFromKeyword(tokens[0]),
		})
	}
	return out, nil
}

func kindFromKeyword(kw string) StatementKind {
	switch kw {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "WITH", "VALUES":
		return KindDML
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "COMMENT", "GRANT", "REVOKE", "REINDEX":
		return KindDDL
	case "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "START", "ABORT":
		return KindTxControl
	default:
		return KindOther
	}
}

func splitRaw(s string) ([]string, error) {
	var parts []string
	start := 0
	for i := 0; i < len(s); {
		next, boundary, err := skipToken(s, i)
		if err != nil {
			return nil, err
		}
		if boundary {
			parts = append(parts, s[start:i])
			start = next
		}
		i = next
	}
	parts = append(parts, s[start:])
	return parts, nil
}

// skipToken advances one lexical chunk. boundary is true when i pointed at a statement semicolon.
func skipToken(s string, i int) (next int, boundary bool, err error) {
	if i >= len(s) {
		return i, false, nil
	}
	switch {
	case s[i] == '-' && i+1 < len(s) && s[i+1] == '-':
		return skipLineComment(s, i), false, nil
	case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
		n, err := skipBlockComment(s, i)
		return n, false, err
	case s[i] == '\'':
		n, err := skipSingleQuote(s, i, isEscapeString(s, i))
		return n, false, err
	case s[i] == '"':
		n, err := skipDoubleQuote(s, i)
		return n, false, err
	case s[i] == '$':
		if closer, after, ok := matchDollar(s, i); ok {
			rel := strings.Index(s[after:], closer)
			if rel < 0 {
				return 0, false, errUnclosed
			}
			return after + rel + len(closer), false, nil
		}
		return i + 1, false, nil
	case s[i] == ';':
		return i + 1, true, nil
	default:
		return i + 1, false, nil
	}
}

func lexKeywords(s string) ([]string, error) {
	var tokens []string
	for i := 0; i < len(s); {
		if isIdentStart(s[i]) {
			j := i + 1
			for j < len(s) && isIdentCont(s[j]) {
				j++
			}
			tokens = append(tokens, strings.ToUpper(s[i:j]))
			i = j
			continue
		}
		next, _, err := skipToken(s, i)
		if err != nil {
			return nil, err
		}
		if next == i {
			i++
			continue
		}
		i = next
	}
	return tokens, nil
}

func skipLineComment(s string, i int) int {
	i += 2
	for i < len(s) && s[i] != '\n' {
		i++
	}
	return i
}

func skipBlockComment(s string, i int) (int, error) {
	i += 2
	for i+1 < len(s) {
		if s[i] == '*' && s[i+1] == '/' {
			return i + 2, nil
		}
		i++
	}
	return 0, errUnclosed
}

func skipSingleQuote(s string, i int, escape bool) (int, error) {
	i++
	for i < len(s) {
		if escape && s[i] == '\\' {
			if i+1 >= len(s) {
				return 0, errUnclosed
			}
			i += 2
			continue
		}
		if s[i] == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' {
				i += 2
				continue
			}
			return i + 1, nil
		}
		i++
	}
	return 0, errUnclosed
}

func skipDoubleQuote(s string, i int) (int, error) {
	i++
	for i < len(s) {
		if s[i] == '"' {
			if i+1 < len(s) && s[i+1] == '"' {
				i += 2
				continue
			}
			return i + 1, nil
		}
		i++
	}
	return 0, errUnclosed
}

func matchDollar(s string, i int) (closer string, next int, ok bool) {
	if i >= len(s) || s[i] != '$' {
		return "", 0, false
	}
	j := i + 1
	if j < len(s) && isIdentStart(s[j]) {
		j++
		for j < len(s) && isIdentCont(s[j]) {
			j++
		}
	}
	if j < len(s) && s[j] == '$' {
		return s[i : j+1], j + 1, true
	}
	return "", 0, false
}

func isEscapeString(s string, i int) bool {
	if i == 0 || s[i] != '\'' {
		return false
	}
	c := s[i-1]
	if c != 'E' && c != 'e' {
		return false
	}
	if i == 1 {
		return true
	}
	return !isIdentCont(s[i-2])
}

func isIdentStart(b byte) bool {
	return unicode.IsLetter(rune(b)) || b == '_'
}

func isIdentCont(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}
