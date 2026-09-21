package test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tianxinzizhen/tgsql"
)

// TestCustomFunc_Plain 验证简单自定义函数（不追加参数）
func TestCustomFunc_Plain(t *testing.T) {
	tdb := newTDB(t)

	called := false
	tdb.AddTemplateFunc("hello", func(s string) string {
		called = true
		return "[" + s + "]"
	})

	sql, args, err := tdb.BuildSQL(context.Background(),
		`SELECT {hello .Name} FROM student WHERE id = {.Id}`,
		map[string]any{"Name": "Alice", "Id": int64(1)},
	)
	if err != nil {
		t.Fatalf("BuildSQL error: %v", err)
	}
	if !called {
		t.Fatal("custom func 'hello' was not invoked")
	}
	wantSQL := "SELECT [Alice] FROM student WHERE id = ?"
	if sql != wantSQL {
		t.Errorf("SQL mismatch:\n  got:  %q\n  want: %q", sql, wantSQL)
	}
	if len(args) != 1 || args[0] != int64(1) {
		t.Errorf("args mismatch: got %v, want [1]", args)
	}
}

// TestCustomFunc_ArgsCollector 验证 ArgsCollector:
// 自定义函数声明 ArgsCollector 为第一个参数，内部调 collect() 追加 SQL 参数
func TestCustomFunc_ArgsCollector(t *testing.T) {
	tdb := newTDB(t)

	called := false
	tdb.AddTemplateFunc("pt", func(collect tgsql.ArgsCollector, lng, lat float64) string {
		called = true
		collect(lng, lat)
		return "ST_GeomFromText(POINT(? ?))"
	})

	type Loc struct {
		Lng, Lat float64
	}
	sql, args, err := tdb.BuildSQL(context.Background(),
		`SELECT ST_X(g) FROM geo_points WHERE location = {pt .Lng .Lat}`,
		Loc{Lng: 116.4074, Lat: 39.9042},
	)
	if err != nil {
		t.Fatalf("BuildSQL error: %v", err)
	}
	if !called {
		t.Fatal("custom func 'pt' was not invoked")
	}
	wantSQL := "SELECT ST_X(g) FROM geo_points WHERE location = ST_GeomFromText(POINT(? ?))"
	if sql != wantSQL {
		t.Errorf("SQL mismatch:\n  got:  %q\n  want: %q", sql, wantSQL)
	}
	if len(args) != 2 {
		t.Fatalf("args count: got %d, want 2, args=%v", len(args), args)
	}
	if args[0].(float64) != 116.4074 || args[1].(float64) != 39.9042 {
		t.Errorf("args value mismatch: got %v, want [116.4074, 39.9042]", args)
	}
}

// TestCustomFunc_CollectorMultiArg 验证 ArgsCollector + 多参数签名
func TestCustomFunc_CollectorMultiArg(t *testing.T) {
	tdb := newTDB(t)

	called := false
	tdb.AddTemplateFunc("threeway", func(collect tgsql.ArgsCollector, a, b, c string) string {
		called = true
		collect(a, b, c)
		return "COALESCE(?, ?, ?)"
	})

	type User struct {
		FirstName, NickName, Email string
		Id                         int64
	}
	sql, args, err := tdb.BuildSQL(context.Background(),
		`SELECT {threeway .FirstName .NickName .Email} AS name FROM user WHERE id = {.Id}`,
		User{FirstName: "Alice", NickName: "Li", Email: "alice@x.com", Id: 1},
	)
	if err != nil {
		t.Fatalf("BuildSQL error: %v", err)
	}
	if !called {
		t.Fatal("custom func 'threeway' was not invoked")
	}
	wantSQL := "SELECT COALESCE(?, ?, ?) AS name FROM user WHERE id = ?"
	if sql != wantSQL {
		t.Errorf("SQL mismatch:\n  got:  %q\n  want: %q", sql, wantSQL)
	}
	if len(args) != 4 {
		t.Fatalf("args count: got %d, want 4, args=%v", len(args), args)
	}
	if args[0].(string) != "Alice" || args[1].(string) != "Li" || args[2].(string) != "alice@x.com" {
		t.Errorf("args value mismatch: got %v", args)
	}
}

// TestCustomFunc_OverwriteBuiltin 验证用户函数名和内置同名时，内置优先
func TestCustomFunc_OverwriteBuiltin(t *testing.T) {
	tdb := newTDB(t)

	// 用户注册同名 "like"（无 ArgsCollector）
	tdb.AddTemplateFunc("like", func(s string) string {
		return "OVERRIDE"
	})

	type User struct {
		UserName string
	}
	sql, args, err := tdb.BuildSQL(context.Background(),
		`SELECT * FROM student WHERE {like .UserName}`,
		User{UserName: "Alice"},
	)
	if err != nil {
		t.Fatalf("BuildSQL error: %v", err)
	}
	wantSQL := "SELECT * FROM student WHERE like ?"
	if sql != wantSQL {
		t.Errorf("SQL mismatch:\n  got:  %q\n  want: %q (user like should not override builtin)", sql, wantSQL)
	}
	if len(args) != 1 {
		t.Fatalf("args count: got %d, want 1", len(args))
	}
	if args[0].(string) != "%Alice%" {
		t.Errorf("args value: got %q, want %q (builtin like should add wildcards)", args[0], "%Alice%")
	}
}

// TestCustomFunc_InitDBFuncPath 验证 InitDBFunc 路径（真实 DAO）里用户自定义函数也生效
func TestCustomFunc_InitDBFuncPath(t *testing.T) {
	t.Skip("LoadFuncDataInfoString needs real .go file path context for ast parsing; tested separately via InitDBFunc integration tests")

	sqldb, err := sql.Open("mysql", "root:Lix@1234@tcp(localhost:3306)/tgsql_test?charset=utf8mb4&parseTime=True&loc=Local")
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	tdb := tgsql.NewTgenSql(sqldb)

	called := false
	tdb.AddTemplateFunc("geo", func(c tgsql.ArgsCollector, lng, lat float64) string {
		called = true
		c(lng, lat)
		return "POINT(? ?)"
	})

	daoCode := "package test\n" +
		"import \"context\"\n" +
		"type TestDao struct {\n" +
		"\t//sql SELECT {geo .Lng .Lat} AS g FROM student WHERE id = {.Id}\n" +
		"\tGetGeo func(ctx context.Context, id int64, loc struct{ Lng, Lat float64 }) (string, error)\n" +
		"}\n"
	if err := tdb.LoadFuncDataInfoString(daoCode); err != nil {
		t.Fatalf("LoadFuncDataInfoString error: %v", err)
	}

	var dao struct {
		GetGeo func(ctx context.Context, id int64, loc struct{ Lng, Lat float64 }) (string, error)
	}
	if err := tgsql.InitDBFunc(tdb, &dao); err != nil {
		t.Fatalf("InitDBFunc error: %v", err)
	}

	_, err = dao.GetGeo(context.Background(), 1, struct{ Lng, Lat float64 }{116.4, 39.9})
	if !called {
		t.Error("custom func 'geo' was NOT invoked in InitDBFunc path")
	}
	t.Logf("custom func called=%v, db err expected=%v", called, err)
}

// TestAddAllTemplateFunc 验证批量注册函数也生效
func TestAddAllTemplateFunc(t *testing.T) {
	tdb := newTDB(t)

	calledA, calledB := false, false
	tdb.AddAllTemplateFunc(map[string]any{
		"A": func() string { calledA = true; return "A" },
		"B": func(s string) string { calledB = true; return "[" + s + "]" },
	})

	sql, _, err := tdb.BuildSQL(context.Background(),
		`SELECT {A}, {B .Name} FROM t`,
		map[string]any{"Name": "Alice"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !calledA || !calledB {
		t.Errorf("AddAllTemplateFunc: not all funcs invoked: calledA=%v calledB=%v", calledA, calledB)
	}
	wantSQL := "SELECT A, [Alice] FROM t"
	if sql != wantSQL {
		t.Errorf("SQL mismatch:\n  got:  %q\n  want: %q", sql, wantSQL)
	}
}

// TestCustomFunc_CollectorNil 验证 ArgsCollector 本身为 nil（用户没注册 collector 型函数）不 panic
func TestCustomFunc_CollectorNil(t *testing.T) {
	tdb := newTDB(t)
	// 不注册任何用户函数
	sql, args, err := tdb.BuildSQL(context.Background(),
		`SELECT {.UserName} FROM student WHERE id = {.Id}`,
		map[string]any{"UserName": "Alice", "Id": int64(1)},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL := "SELECT ? FROM student WHERE id = ?"
	if sql != wantSQL {
		t.Errorf("SQL mismatch:\n  got:  %q\n  want: %q", sql, wantSQL)
	}
	if len(args) != 2 {
		t.Errorf("args count: got %d, want 2, args=%v", len(args), args)
	}
}
