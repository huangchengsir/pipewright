package store

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// 错误分类集中实现:外键/唯一冲突在 SQLite 与 MySQL 上的"签名"不同——
// SQLite(modernc) 把约束名写进错误文本,MySQL 用数字错误码。
// 这两个函数靠错误自身携带的方言签名判定,同一函数同时识别两种来源,
// 调用方无需知道当前方言。各领域包统一转调这里,避免 9 处文本匹配在 MySQL 上失效。
//
// MySQL 错误码:1062 = 唯一键冲突(ER_DUP_ENTRY);
// 1451 = 删父行被子行外键挡(ER_ROW_IS_REFERENCED_2);
// 1452 = 插/改子行无对应父行(ER_NO_REFERENCED_ROW_2)。

// IsUniqueErr 判断错误是否为唯一约束冲突。
func IsUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1062
	}
	return strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}

// IsForeignKeyErr 判断错误是否为外键约束失败。
func IsForeignKeyErr(err error) bool {
	if err == nil {
		return false
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1451 || me.Number == 1452
	}
	return strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY")
}
