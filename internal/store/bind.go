package store

import (
	"fmt"
	"strconv"
	"strings"
)

// Bind changes only parameter tokens. Quoted strings/identifiers, comments and
// PostgreSQL dollar-quoted bodies are opaque. Use ?? for a literal SQL ? operator.
func Bind(d Dialect, query string) (string, error) {
	if d == SQLite || d == MySQL {
		return query, nil
	}
	if d != PostgreSQL && d != DM && d != SQLServer {
		return "", fmt.Errorf("store: unsupported dialect")
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); {
		end, opaque, err := sqlToken(query, i)
		if d == SQLServer && query[i] == '[' {
			end, opaque, err = sqlServerBracket(query, i)
		}
		if err != nil {
			return "", err
		}
		if !opaque && query[i] == '?' {
			if i+1 < len(query) && query[i+1] == '?' {
				b.WriteByte('?')
				i += 2
				continue
			}
			n++
			if d == PostgreSQL {
				b.WriteByte('$')
			} else if d == SQLServer {
				b.WriteString("@p")
			} else {
				b.WriteByte(':')
			}
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteString(query[i:end])
		}
		i = end
	}
	return b.String(), nil
}

// sqlToken identifies opaque SQL regions without interpreting or rewriting SQL.
func sqlToken(s string, i int) (end int, opaque bool, err error) {
	if i+1 < len(s) && s[i:i+2] == "--" {
		end = strings.IndexByte(s[i:], '\n')
		if end < 0 {
			return len(s), true, nil
		}
		return i + end + 1, true, nil
	}
	if i+1 < len(s) && s[i:i+2] == "/*" {
		depth := 1
		for j := i + 2; j < len(s)-1; j++ {
			if s[j:j+2] == "/*" {
				depth++
				j++
			} else if s[j:j+2] == "*/" {
				depth--
				j++
				if depth == 0 {
					return j + 1, true, nil
				}
			}
		}
		return 0, false, fmt.Errorf("store: unterminated SQL comment")
	}
	if s[i] == '\'' || s[i] == '"' || s[i] == '`' {
		quote := s[i]
		escapes := quote == '\'' && i > 0 && (s[i-1] == 'E' || s[i-1] == 'e') && (i == 1 || !sqlIdent(s[i-2]))
		for j := i + 1; j < len(s); j++ {
			if escapes && s[j] == '\\' {
				j++
				continue
			}
			if s[j] == quote {
				if j+1 < len(s) && s[j+1] == quote {
					j++
					continue
				}
				return j + 1, true, nil
			}
		}
		return 0, false, fmt.Errorf("store: unterminated SQL quote")
	}
	if s[i] == '$' && (i == 0 || !sqlIdent(s[i-1])) {
		j := i + 1
		for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || j > i+1 && s[j] >= '0' && s[j] <= '9') {
			j++
		}
		if j < len(s) && s[j] == '$' {
			tag := s[i : j+1]
			k := strings.Index(s[j+1:], tag)
			if k < 0 {
				return 0, false, fmt.Errorf("store: unterminated SQL dollar quote")
			}
			return j + 1 + k + len(tag), true, nil
		}
	}
	return i + 1, false, nil
}
func sqlIdent(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func splitSQLStatements(s string) ([]string, error) {
	var statements []string
	start := 0
	for i := 0; i < len(s); {
		end, opaque, err := sqlToken(s, i)
		if err != nil {
			return nil, err
		}
		if !opaque && s[i] == ';' {
			if part := strings.TrimSpace(s[start:i]); part != "" {
				statements = append(statements, part)
			}
			start = end
		}
		i = end
	}
	if part := strings.TrimSpace(s[start:]); part != "" {
		statements = append(statements, part)
	}
	return statements, nil
}

func sqlServerBracket(s string, i int) (int, bool, error) {
	for j := i + 1; j < len(s); j++ {
		if s[j] == ']' {
			if j+1 < len(s) && s[j+1] == ']' {
				j++
				continue
			}
			return j + 1, true, nil
		}
	}
	return 0, false, fmt.Errorf("store: unterminated quoted identifier")
}
