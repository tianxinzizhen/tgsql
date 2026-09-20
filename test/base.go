package test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tianxinzizhen/tgsql"

	_ "github.com/go-sql-driver/mysql"
)

// Student 学生模型
type Student struct {
	Id       int64
	UserName string
	Age      int
}

// newTDB 创建连接到 tgsql_test 的 TgenSql 实例
func newTDB(t *testing.T) *tgsql.TgenSql {
	t.Helper()
	sqldb, err := sql.Open("mysql", "root:Lix@1234@tcp(localhost:3306)/tgsql_test?charset=utf8mb4&parseTime=True&loc=Local")
	if err != nil {
		t.Fatal(err)
	}
	tdb := tgsql.NewTgenSql(sqldb)
	tdb.SqlLogFunc(func(ctx context.Context, funcName, sql string, args ...any) {
		t.Logf("  SQL> %s args=%v", sql, args)
	})
	return tdb
}

// testDB 创建原生 *sql.DB 连接
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", "root:Lix@1234@tcp(localhost:3306)/tgsql_test?charset=utf8mb4&parseTime=True&loc=Local")
	if err != nil {
		t.Fatal(err)
	}
	return db
}
