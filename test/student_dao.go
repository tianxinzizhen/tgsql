package test

import (
	"context"
	"database/sql"
	"embed"
	"testing"

	"github.com/tianxinzizhen/tgsql"
)

//go:embed *.go
var testDaoFS embed.FS

// StudentDao 学生数据访问层 — 用 //sql 注解定义 SQL 模板
type StudentDao struct {
	//sql INSERT INTO student (user_name, age) VALUES(?)
	Insert func(ctx context.Context, s *Student) (sql.Result, error)

	//sql SELECT * FROM student WHERE id = {.id}
	GetByID func(ctx context.Context, id int64) (*Student, error)

	//sql SELECT * FROM student WHERE id IN {in .}
	GetByIds func(ctx context.Context, ids []int64) ([]*Student, error)

	/*sql
	SELECT * FROM student WHERE 1=1
	[AND age > {.age}]
	[AND user_name {like .Name}]
	*/
	List func(ctx context.Context, age int, name string) ([]*Student, error)

	//sql UPDATE student SET {set .} WHERE id = {.Id}
	Update func(ctx context.Context, s *Student) error

	//sql DELETE FROM student WHERE id = {.id}
	Delete func(ctx context.Context, id int64) error
}

// NewStudentDao 工厂函数 — 加载 //sql 注释 + InitDBFunc 绑定
func NewStudentDao(t *testing.T) *StudentDao {
	t.Helper()
	tdb := newTDB(t)

	if err := tdb.LoadFuncDataInfo(testDaoFS); err != nil {
		t.Fatalf("LoadFuncDataInfo: %v", err)
	}

	ret := &StudentDao{}
	if err := tgsql.InitDBFunc(tdb, ret); err != nil {
		t.Fatalf("InitDBFunc: %v", err)
	}
	return ret
}
