package store

import (
	"database/sql"
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
)

// String 返回方言名,便于日志与错误信息。
func (d Dialect) String() string {
	if d == MySQL {
		return "mysql"
	}
	return "sqlite"
}

// DialectOf 通过 *sql.DB 的底层驱动类型判定方言。无全局可变态:
// 同进程多库(如测试)各自独立判定。非 MySQL 驱动一律视为 SQLite。
func DialectOf(db *sql.DB) Dialect {
	if _, ok := db.Driver().(*mysql.MySQLDriver); ok {
		return MySQL
	}
	return SQLite
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
