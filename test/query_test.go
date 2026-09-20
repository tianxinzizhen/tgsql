package test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestQuerySimple 基础查询：{id} → {param .Id}
func TestQuerySimple(t *testing.T) {
	tdb := newTDB(t)
	ctx := context.Background()

	sqlStr, args, err := tdb.BuildSQL(ctx, "SELECT * FROM student WHERE id = {id}",
		map[string]any{"Id": int64(1)})
	if err != nil {
		t.Fatal(err)
	}

	db := testDB(t)
	defer db.Close()
	rows, err := db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var id int64
		var userName string
		var age int
		var email sql.NullString
		var createTime sql.NullTime
		if err := rows.Scan(&id, &userName, &age, &email, &createTime); err != nil {
			t.Fatal(err)
		}
		count++
		if id != 1 || userName != "Alice" {
			t.Errorf("Expected id=1, userName=Alice, got id=%d, userName=%s", id, userName)
		}
	}
	if count != 1 {
		t.Errorf("Expected 1 row, got %d", count)
	}
}

// TestQueryOptionalCondition 可选条件：[AND age > {age}]
func TestQueryOptionalCondition(t *testing.T) {
	tdb := newTDB(t)
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	run := func(label, sqlTpl string, params any, minRows int) {
		sqlStr, args, err := tdb.BuildSQL(ctx, sqlTpl, params)
		if err != nil {
			t.Fatalf("[%s] BuildSQL: %v", label, err)
		}
		rows, err := db.QueryContext(ctx, sqlStr, args...)
		if err != nil {
			t.Fatalf("[%s] Query: %v", label, err)
		}
		defer rows.Close()
		var count int
		for rows.Next() {
			count++
		}
		if count < minRows {
			t.Errorf("[%s] Expected >= %d rows, got %d  (SQL=%s)", label, minRows, count, sqlStr)
		}
	}

	// age > 25 → 至少 Carol(30), David(35), Eve(28)
	run("only_age",
		"SELECT * FROM student WHERE 1=1 [AND age > {age}]",
		map[string]any{"Age": 25},
		3)

	// Age=0 → 零值不拼接
	run("no_conditions",
		"SELECT * FROM student WHERE 1=1 [AND age > {age}]",
		map[string]any{"Age": 0},
		5)
}

// TestQueryStructWhere where 函数 + struct 反射 → snake_case 列名
func TestQueryStructWhere(t *testing.T) {
	tdb := newTDB(t)
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	sqlStr, args, err := tdb.BuildSQL(ctx, "SELECT * FROM student WHERE {where .cond}",
		map[string]any{"cond": Student{Age: 25}})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("SQL: %s, Args: %v", sqlStr, args)

	if strings.Contains(sqlStr, "Age") {
		t.Errorf("WHERE 条件不应包含 PascalCase 'Age': %s", sqlStr)
	}
	if !strings.Contains(sqlStr, "age") {
		t.Errorf("WHERE 条件应有 snake_case 'age': %s", sqlStr)
	}

	rows, err := db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("Expected 1 row with age=25 (Bob), got %d", count)
	}
}

// TestQueryInsertValuesExpand INSERT VALUES(?) 隐式展开
func TestQueryInsertValuesExpand(t *testing.T) {
	tdb := newTDB(t)
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	db.ExecContext(ctx, "DELETE FROM student WHERE user_name = ?", "Frank")

	sqlStr, args, err := tdb.BuildSQL(ctx, "INSERT INTO student (user_name, age) VALUES(?)",
		map[string]any{
			"UserName": "Frank",
			"Age":      22,
		})
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}

	t.Logf("SQL: %s, Args: %v", sqlStr, args)
	if len(args) != 2 {
		t.Fatalf("Expected 2 args, got %d: %v", len(args), args)
	}

	result, err := db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		t.Fatal(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		t.Errorf("Expected 1 affected row, got %d", affected)
	}

	var id int64
	var userName string
	var age int
	err = db.QueryRowContext(ctx, "SELECT id, user_name, age FROM student WHERE user_name = ?", "Frank").
		Scan(&id, &userName, &age)
	if err != nil {
		t.Fatal(err)
	}
	if userName != "Frank" || age != 22 {
		t.Errorf("Wrong data: name=%s, age=%d", userName, age)
	}
	t.Logf("✓ Insert verified: id=%d, name=%s, age=%d", id, userName, age)

	db.ExecContext(ctx, "DELETE FROM student WHERE id = ?", id)
}

// TestQueryLikeIn like 和 in 隐式转换
func TestQueryLikeIn(t *testing.T) {
	tdb := newTDB(t)
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	t.Run("like", func(t *testing.T) {
		sqlStr, args, err := tdb.BuildSQL(ctx, "SELECT * FROM student WHERE user_name like ?",
			map[string]any{"UserName": "a"})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("SQL: %s, Args: %v", sqlStr, args)

		if len(args) != 1 {
			t.Fatalf("Expected 1 arg, got %d", len(args))
		}
		if args[0] != "%a%" {
			t.Errorf("Expected like arg %q, got %q", "%a%", args[0])
		}

		rows, err := db.QueryContext(ctx, sqlStr, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var count int
		for rows.Next() {
			count++
		}
		t.Logf("Rows: %d", count)
	})

	t.Run("in", func(t *testing.T) {
		sqlStr, args, err := tdb.BuildSQL(ctx, "SELECT * FROM student WHERE id in ?",
			map[string]any{"Id": []int64{1, 2, 3}})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("SQL: %s, Args: %v", sqlStr, args)

		rows, err := db.QueryContext(ctx, sqlStr, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var count int
		for rows.Next() {
			count++
		}
		if count != 3 {
			t.Errorf("Expected 3 rows for id in [1,2,3], got %d", count)
		}
	})
}

// TestQueryUpdateSet UPDATE + set 函数 + snake_case 列名
func TestQueryUpdateSet(t *testing.T) {
	tdb := newTDB(t)
	ctx := context.Background()
	db := testDB(t)
	defer db.Close()

	db.ExecContext(ctx, "UPDATE student SET user_name='Alice', age=20 WHERE id=1")

	sqlStr, args, err := tdb.BuildSQL(ctx, "UPDATE student {set .s} WHERE id = {id}",
		map[string]any{
			"s":  Student{UserName: "Alice_New", Age: 21},
			"Id": int64(1),
		})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("SQL: %s, Args: %v", sqlStr, args)

	if strings.Contains(sqlStr, "UserName") {
		t.Errorf("SET 子句不应包含 PascalCase 列名: %s", sqlStr)
	}
	if !strings.Contains(sqlStr, "user_name") || !strings.Contains(sqlStr, "age") {
		t.Errorf("SET 子句应有 snake_case 列名: %s", sqlStr)
	}

	result, err := db.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		t.Fatal(err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		t.Errorf("Expected 1 affected row, got %d", affected)
	}

	var userName string
	var age int
	err = db.QueryRowContext(ctx, "SELECT user_name, age FROM student WHERE id = 1").Scan(&userName, &age)
	if err != nil {
		t.Fatal(err)
	}
	if userName != "Alice_New" || age != 21 {
		t.Errorf("Update failed: name=%s, age=%d", userName, age)
	}
	t.Logf("✓ Update verified: name=%s, age=%d", userName, age)

	db.ExecContext(ctx, "UPDATE student SET user_name='Alice', age=20 WHERE id=1")
}

// TestQueryMultiField 多字段引用 {id, user_name}
func TestQueryMultiField(t *testing.T) {
	tdb := newTDB(t)
	ctx := context.Background()

	sqlStr, args, err := tdb.BuildSQL(ctx, "SELECT {id, user_name} FROM student WHERE id = {id}",
		map[string]any{"Id": int64(1), "UserName": "Alice"})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("SQL: %s, Args: %v", sqlStr, args)
	// {id, user_name} → {param .Id}, {param .UserName}，加上 WHERE 的 {param .Id} → 3 个 args
	if len(args) != 3 {
		t.Errorf("Expected 3 args, got %d", len(args))
	}
}
