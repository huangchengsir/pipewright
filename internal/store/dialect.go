package store

import (
	"database/sql"
	"fmt"
	"modernc.org/sqlite"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// Dialect 标识底层数据库方言。upsert / 自增 / 标识符引用等存在跨库分歧的 SQL,
// 由本包的方言 helper 按 Dialect 生成,使各领域包无需感知具体数据库。
type Dialect int

const (
	// SQLite 是默认方言(modernc.org/sqlite,无 CGO)。
	SQLite Dialect = iota
	// MySQL 是可选方言(github.com/go-sql-driver/mysql,8.0+)。
	MySQL
	PostgreSQL
	DM
	SQLServer
	Unknown Dialect = -1
)

// String 返回方言名,便于日志与错误信息。
func (d Dialect) String() string {
	switch d {
	case SQLite:
		return "sqlite"
	case MySQL:
		return "mysql"
	case PostgreSQL:
		return "postgres"
	case DM:
		return "dm"
	case SQLServer:
		return "sqlserver"
	default:
		return "unknown"
	}
}

// DialectOf never treats an unrecognized driver as SQLite.
func DialectOf(db *sql.DB) Dialect {
	if db == nil {
		return Unknown
	}
	switch d := db.Driver().(type) {
	case *bindingDriver:
		return d.dialect
	case *mysql.MySQLDriver:
		return MySQL
	case *sqlite.Driver:
		return SQLite
	default:
		return Unknown
	}
}

func (d Dialect) RowLocks() bool { return d == MySQL || d == PostgreSQL || d == DM || d == SQLServer }
func ByteLength(d Dialect, column string) string {
	switch d {
	case SQLite:
		return "LENGTH(CAST(" + column + " AS BLOB))"
	case SQLServer:
		return "DATALENGTH(CONVERT(VARCHAR(MAX), " + column + " COLLATE Latin1_General_100_BIN2_UTF8))"
	case MySQL, PostgreSQL, DM:
		return "OCTET_LENGTH(" + column + ")"
	default:
		panic("store: unsupported byte length dialect")
	}
}

// InsertSQL builds a complete statement, including MERGE for DM. Every identifier
// and value expression is a source-code constant; row values stay bound args.
// A nil update list means do-nothing on the specified unique key.
func InsertSQL(d Dialect, table string, columns, values, conflict, updates []string) string {
	if len(columns) == 0 || len(columns) != len(values) || len(conflict) == 0 {
		panic("store: invalid insert shape")
	}
	checkIdentifier(table)
	known := map[string]bool{}
	for _, col := range columns {
		checkIdentifier(col)
		if known[col] {
			panic("store: duplicate insert column")
		}
		known[col] = true
	}
	for _, col := range append(append([]string{}, conflict...), updates...) {
		if !known[col] {
			panic("store: conflict/update column missing from insert")
		}
	}
	if d == DM || d == SQLServer {
		source := make([]string, len(columns))
		inserted := make([]string, len(columns))
		for i, col := range columns {
			source[i] = values[i] + " AS " + col
			inserted[i] = "incoming." + col
		}
		matches := make([]string, len(conflict))
		for i, col := range conflict {
			matches[i] = "target." + col + "=incoming." + col
		}
		from := " FROM DUAL"
		targetAlias := " target"
		if d == SQLServer {
			from = ""
			targetAlias = " WITH (HOLDLOCK) AS target"
		}
		q := "MERGE INTO " + table + targetAlias + " USING (SELECT " + strings.Join(source, ", ") + from + ") incoming ON (" + strings.Join(matches, " AND ") + ")"
		if len(updates) > 0 {
			assigns := make([]string, len(updates))
			for i, col := range updates {
				assigns[i] = "target." + col + "=incoming." + col
			}
			q += " WHEN MATCHED THEN UPDATE SET " + strings.Join(assigns, ", ")
		}
		q += " WHEN NOT MATCHED THEN INSERT (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(inserted, ", ") + ")"
		if d == SQLServer {
			q += ";"
		}
		return q
	}
	if d != SQLite && d != MySQL && d != PostgreSQL {
		panic("store: unsupported insert dialect")
	}
	q := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(values, ", ") + ") "
	if len(updates) == 0 {
		return q + DoNothingSuffix(d, conflict)
	}
	return q + UpsertSuffix(d, conflict, updates)
}
func checkIdentifier(s string) {
	if s == "" {
		panic("store: empty SQL identifier")
	}
	for i, c := range []byte(s) {
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			panic(fmt.Sprintf("store: invalid SQL identifier %q", s))
		}
	}
}

// Excluded 返回"待插入行某列值"的方言表达式,用于 upsert 的 SET 右值。
//
//	SQLite: excluded.<col>
//	MySQL : VALUES(<col>)   (MySQL 8.0 全系可用)
//
// 仅在需要混合字面量与"取插入值"的赋值场景(如 approval CreatePending)直接调用;
// 纯"col = 插入值"的常见场景用 UpsertSuffix。
func Excluded(d Dialect, col string) string {
	if d == MySQL {
		return "VALUES(" + col + ")"
	}
	return "excluded." + col
}

// UpsertSuffix 生成"主键/唯一键冲突即更新"的尾句,每个 updateCols 均为
// "col = <插入行的 col>" 形式(最常见场景)。
//
//	SQLite: ON CONFLICT(<conflictCols>) DO UPDATE SET c = excluded.c, ...
//	MySQL : ON DUPLICATE KEY UPDATE c = VALUES(c), ...
func UpsertSuffix(d Dialect, conflictCols, updateCols []string) string {
	assigns := make([]string, len(updateCols))
	for i, c := range updateCols {
		assigns[i] = c + " = " + Excluded(d, c)
	}
	return UpsertAssignSuffix(d, conflictCols, assigns)
}

// UpsertAssignSuffix 生成"冲突即更新"尾句,assignments 为完整的 "lhs = rhs" 赋值串
// (rhs 可为字面量或 Excluded(d,col))。用于赋值混合了常量与插入值的场景。
//
//	SQLite: ON CONFLICT(<conflictCols>) DO UPDATE SET <assignments...>
//	MySQL : ON DUPLICATE KEY UPDATE <assignments...>
func UpsertAssignSuffix(d Dialect, conflictCols, assignments []string) string {
	set := strings.Join(assignments, ", ")
	if d == MySQL {
		return "ON DUPLICATE KEY UPDATE " + set
	}
	return "ON CONFLICT(" + strings.Join(conflictCols, ", ") + ") DO UPDATE SET " + set
}

// DoNothingSuffix 生成"冲突即忽略"的尾句。
//
//	SQLite: ON CONFLICT(<conflictCols>) DO NOTHING
//	MySQL : ON DUPLICATE KEY UPDATE <c0> = <c0>   (自赋值 no-op,等价 DO NOTHING)
//
// 调用点统一靠回读权威行兜并发首访,不依赖 RowsAffected,故 no-op 完全安全。
func DoNothingSuffix(d Dialect, conflictCols []string) string {
	if d == MySQL {
		c := conflictCols[0]
		return "ON DUPLICATE KEY UPDATE " + c + " = " + c
	}
	return "ON CONFLICT(" + strings.Join(conflictCols, ", ") + ") DO NOTHING"
}

// Identifier quotes a fixed, validated schema identifier, including reserved
// words such as servers.user. Values must still use bound parameters.
func Identifier(d Dialect, name string) string {
	checkIdentifier(name)
	switch d {
	case SQLite, MySQL:
		return "`" + name + "`"
	case PostgreSQL:
		return "\"" + name + "\""
	case SQLServer:
		return "[" + name + "]"
	case DM:
		return "\"" + strings.ToUpper(name) + "\""
	default:
		panic("store: unsupported identifier dialect")
	}
}

// Limit is appended to an explicit ORDER BY query. count is a source constant
// decimal or a bound placeholder, never untrusted SQL.
func Limit(d Dialect, count string) string {
	if count != "?" {
		if count == "" {
			panic("store: empty limit")
		}
		for _, c := range count {
			if c < '0' || c > '9' {
				panic("store: invalid limit")
			}
		}
	}
	if d == SQLServer {
		return " OFFSET 0 ROWS FETCH NEXT " + count + " ROWS ONLY"
	}
	return " LIMIT " + count
}

// Page returns the explicit suffix and parameter order for an ORDER BY query.
func Page(d Dialect, limit, offset int) (string, []any) {
	if d == SQLServer {
		return " OFFSET ? ROWS FETCH NEXT ? ROWS ONLY", []any{offset, limit}
	}
	return " LIMIT ? OFFSET ?", []any{limit, offset}
}
func LockHint(d Dialect, lock bool) string {
	if d == SQLServer && lock {
		return " WITH (UPDLOCK,HOLDLOCK)"
	}
	return ""
}
func LockSuffix(d Dialect, lock bool) string {
	if lock && d.RowLocks() && d != SQLServer {
		return " FOR UPDATE"
	}
	return ""
}

func BlobLength(d Dialect, column string) string {
	if d == SQLServer {
		return "DATALENGTH(" + column + ")"
	}
	return ByteLength(d, column)
}

// BinaryPlaceholder gives an untyped nil a binary SQL type on SQL Server.
// []byte(nil), []byte{} and nil retain their distinct NULL/empty semantics.
// This is selected from an explicit column type, never inferred by rewriting SQL.
func BinaryPlaceholder(d Dialect) string {
	if d == SQLServer {
		return "CAST(? AS VARBINARY(MAX))"
	}
	return "?"
}

// BlobEqual compares a complete binary value with one bound parameter. DM does
// not support BLOB equality with =; COMPARE retains the compare-and-swap guard
// without changing server compatibility settings or truncating either value.
// A NULL operand does not match, as with ordinary SQL equality.
func BlobEqual(d Dialect, column string) string {
	name := Identifier(d, column)
	if d == DM {
		return "DBMS_LOB.COMPARE(" + name + ", CAST(? AS BLOB)) = 0"
	}
	return name + " = " + BinaryPlaceholder(d)
}
