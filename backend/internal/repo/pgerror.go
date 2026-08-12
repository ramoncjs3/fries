package repo

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation 是 PostgreSQL 唯一约束冲突的 SQLSTATE。
const uniqueViolation = "23505"

// IsUniqueViolation 判断错误是不是指定唯一索引的冲突。
//
// **先 SELECT 查重再 INSERT 是错的** —— 两次查询之间有竞态窗口，并发下照样撞。
// 正确做法是直接写，让数据库判，然后按索引名把 23505 翻成人话。
//
// constraint 传索引名（如 uk_departments_code）。传空只判「是不是唯一冲突」。
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
