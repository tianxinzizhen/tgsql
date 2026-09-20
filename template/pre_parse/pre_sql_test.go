package preparse

import (
	"reflect"
	"strings"
	"testing"
	"text/template"
)

// 辅助函数：比较预处理前后的输出
func assertPreprocess(t *testing.T, input, expected string, cfg Config) {
	t.Helper()
	result := Preprocess(input, cfg)
	if result != expected {
		t.Errorf("Preprocess(%q) =\n  got:  %q\n  want: %q", input, result, expected)
	}
}

func TestBasicFieldReference(t *testing.T) {
	cfg := DefaultConfig() // 恒等转换：保持用户输入原样，但统一 param 包装

	// 基本字段引用 {id} -> {param .id}
	assertPreprocess(t, "{id}", "{param .id}", cfg)
	assertPreprocess(t, "{.id}", "{param .id}", cfg)
	assertPreprocess(t, "{.ID}", "{param .ID}", cfg)
	assertPreprocess(t, "{ID}", "{param .ID}", cfg)

	// 多字段逗号分隔
	assertPreprocess(t, "{id, name, age}",
		"{param .id}, {param .name}, {param .age}", cfg)

	// 多字段空格分隔
	assertPreprocess(t, "{id name age}",
		"{param .id}, {param .name}, {param .age}", cfg)
}

func TestAtSignFieldReference(t *testing.T) {
	cfg := DefaultConfig()

	// {@id} -> {param .id}
	assertPreprocess(t, "{@id}", "{param .id}", cfg)
	assertPreprocess(t, "{@user_name}", "{param .user_name}", cfg)

	// @符号 + 多字段逗号分隔
	assertPreprocess(t, "{@id, @user_name, @age}",
		"{param .id}, {param .user_name}, {param .age}", cfg)

	// SQL体中的@field（不在大括号内）
	assertPreprocess(t, "select * from test where id=@id",
		"select * from test where id={param .id}", cfg)
	assertPreprocess(t, "insert into test values(@id,@name)",
		"insert into test values({param .id},{param .name})", cfg)
}

func TestExistingDotNotation(t *testing.T) {
	cfg := DefaultConfig()

	// 已有正确的点号语法，也应该 param 包装
	assertPreprocess(t, "{.id}", "{param .id}", cfg)
	assertPreprocess(t, "{.ID}", "{param .ID}", cfg)

	// 多字段已有正确点号
	assertPreprocess(t, "{.id, .user_name}",
		"{param .id}, {param .user_name}", cfg)
}

func TestTemplateKeywords(t *testing.T) {
	cfg := DefaultConfig()

	// 模板关键字应该保持原样（不走 param 包装）
	assertPreprocess(t, "{if .Id}", "{if .Id}", cfg)
	assertPreprocess(t, "{if .Id} hello {end}",
		"{if .Id} hello {end}", cfg)
	assertPreprocess(t, "{else}", "{else}", cfg)
	assertPreprocess(t, "{range .items}", "{range .items}", cfg)
}

func TestTemplateFunctions(t *testing.T) {
	cfg := DefaultConfig()

	// 模板函数 — set/where 自动补 SET/WHERE 关键字前缀
	assertPreprocess(t, "{like .user_name}", "{like .user_name}", cfg)
	assertPreprocess(t, "{in .ids}", "{in .ids}", cfg)
	assertPreprocess(t, "{set .user}", "SET {set .user}", cfg)
	assertPreprocess(t, "{where .user}", "WHERE {where .user}", cfg)
	assertPreprocess(t, "{param .id .name}", "{param .id .name}", cfg)
	assertPreprocess(t, "{json .info}", "{json .info}", cfg)
	assertPreprocess(t, "{marshal .info}", "{marshal .info}", cfg)

	// 函数参数中的纯标识符需要转换（加 . 前缀）
	assertPreprocess(t, "{param id name}", "{param .id .name}", cfg)
	assertPreprocess(t, "{in ids}", "{in .ids}", cfg)

	// set带字符串参数 — 仍然自动补 SET 前缀
	assertPreprocess(t, "{set \"u\" .user}", "SET {set \"u\" .user}", cfg)
}

func TestSquareBracketOptionalCondition(t *testing.T) {
	cfg := DefaultConfig()

	// README: 方括号可选条件
	assertPreprocess(t, "[AND id = {id}]",
		"{- if .id} AND id = {param .id}{end -}", cfg)

	assertPreprocess(t, "[AND id = @id]",
		"{- if .id} AND id = {param .id}{end -}", cfg)

	// 方括号 + SQL占位符（隐式转为 param）
	assertPreprocess(t, "[AND id = ?]",
		"{- if .id} AND id = {param .id}{end -}", cfg)

	// 复杂组合
	// 新行为：if 检查优先从模板引用里提取字段（和参数绑定一致），而不是从 SQL 列名推断
	assertPreprocess(t,
		"SELECT * FROM user WHERE 1 = 1 [AND id = {.Id}] [AND user_name {like .keyword}]",
		"SELECT * FROM user WHERE 1 = 1 {- if .Id} AND id = {param .Id}{end -} {- if .keyword} AND user_name {like .keyword}{end -}",
		cfg)
}

func TestFullInsertStatement(t *testing.T) {
	cfg := DefaultConfig()

	// README 中的 INSERT 示例
	input := "INSERT INTO user (id, user_name, age) VALUES ({id}, {user_name}, {age});"
	expected := "INSERT INTO user (id, user_name, age) VALUES ({param .id}, {param .user_name}, {param .age});"
	assertPreprocess(t, input, expected, cfg)

	input2 := "INSERT INTO user (id, user_name, age) VALUES ({id, user_name, age});"
	expected2 := "INSERT INTO user (id, user_name, age) VALUES ({param .id}, {param .user_name}, {param .age});"
	assertPreprocess(t, input2, expected2, cfg)
}

func TestFullSelectWithOptionalConditions(t *testing.T) {
	cfg := DefaultConfig()

	// README 中的完整示例
	input := `SELECT * FROM user WHERE 1 = 1 [AND id = ?] [AND user_name = ?];`
	expected := `SELECT * FROM user WHERE 1 = 1 {- if .id} AND id = {param .id}{end -} {- if .user_name} AND user_name = {param .user_name}{end -};`
	assertPreprocess(t, input, expected, cfg)
}

func TestSQLStringNotAffected(t *testing.T) {
	cfg := DefaultConfig()

	// 字符串内的 @ 和 [] 不应该被转换
	input := "SELECT '@not_a_field' FROM user WHERE name = '{not_a_field}' AND tag = \"[not_a_bracket]\";"
	expected := input
	assertPreprocess(t, input, expected, cfg)
}

func TestBacktickStringNotAffected(t *testing.T) {
	cfg := DefaultConfig()

	input := "SELECT `@col_name` FROM user WHERE `id` = {id};"
	expected := "SELECT `@col_name` FROM user WHERE `id` = {param .id};"
	assertPreprocess(t, input, expected, cfg)
}

func TestSingleTemplatesParseable(t *testing.T) {
	cfg := DefaultConfig()

	// 测试单个模板表达式能被正确 parse（使用注册的函数）
	funcs := template.FuncMap{
		"like":  func(v any) string { return "like ?" },
		"in":    func(v any) string { return "(?)" },
		"set":   func(v ...any) string { return "x=?" },
		"param": func(v ...any) string { return "?" },
		"json":  func(v any) string { return "?" },
	}

	testCases := []string{
		"{.Id}",
		"{- if .Id} hello {end -}",
		"{like .user_name}",
		"{in .Ids}",
		"{param .Id .Name}",
	}

	for _, sql := range testCases {
		t.Run(sql, func(t *testing.T) {
			preprocessed := Preprocess(sql, cfg)
			_, err := template.New("test").Delims(cfg.LeftDelim, cfg.RightDelim).
				Funcs(funcs).Parse(preprocessed)
			if err != nil {
				t.Errorf("Parse error: %v\nPreprocessed: %s", err, preprocessed)
			}
		})
	}
}

// 带完整 mock sqlFunc 的执行测试（验证参数化链路）
type mockSQLFunc struct {
	Args []any
}

func (sq *mockSQLFunc) param(list ...reflect.Value) string {
	sb := &strings.Builder{}
	for i := range list {
		if i > 0 {
			sb.WriteString(",")
		}
		sq.Args = append(sq.Args, list[i].Interface())
		sb.WriteString("?")
	}
	return sb.String()
}

func (sq *mockSQLFunc) like(v reflect.Value) string {
	sq.Args = append(sq.Args, v.Interface())
	return "like ?"
}

func (sq *mockSQLFunc) BuildFuncMap() template.FuncMap {
	return template.FuncMap{
		"param": sq.param,
		"like":  sq.like,
	}
}

func TestExecuteAfterPreprocess(t *testing.T) {
	cfg := DefaultConfig()

	type User struct {
		ID       int64
		UserName string
		Age      int
	}

	testCases := []struct {
		name     string
		sql      string
		params   any
		wantSQL  string // 期望执行后的 SQL（只有 ? 占位符，无值）
		wantArgs []any  // 期望的参数值数组
	}{
		{
			name:     "simple field",
			sql:      "SELECT {.ID} WHERE id = {.ID}",
			params:   User{ID: 123},
			wantSQL:  "SELECT ? WHERE id = ?",
			wantArgs: []any{int64(123), int64(123)},
		},
		{
			name:     "snake_case field",
			sql:      "SELECT {UserName} WHERE id = {.ID}",
			params:   User{ID: 123, UserName: "test"},
			wantSQL:  "SELECT ? WHERE id = ?",
			wantArgs: []any{"test", int64(123)},
		},
		{
			name:     "at sign field in braces",
			sql:      "SELECT {@UserName} WHERE id = @ID",
			params:   User{ID: 456, UserName: "hello"},
			wantSQL:  "SELECT ? WHERE id = ?",
			wantArgs: []any{"hello", int64(456)},
		},
		{
			name:     "optional condition present",
			sql:      "SELECT * FROM user WHERE 1=1 [AND UserName = {.UserName}]",
			params:   User{ID: 1, UserName: "Alice"},
			wantSQL:  "SELECT * FROM user WHERE 1=1 AND UserName = ?",
			wantArgs: []any{"Alice"},
		},
		{
			name:     "optional condition absent",
			sql:      "SELECT * FROM user WHERE 1=1 [AND UserName = {.UserName}]",
			params:   User{ID: 1, UserName: ""},
			wantSQL:  "SELECT * FROM user WHERE 1=1",
			wantArgs: nil,
		},
		{
			name:     "multi field expand",
			sql:      "INSERT INTO user VALUES ({ID, UserName})",
			params:   User{ID: 789, UserName: "Bob"},
			wantSQL:  "INSERT INTO user VALUES (?, ?)",
			wantArgs: []any{int64(789), "Bob"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			preprocessed := Preprocess(tc.sql, cfg)

			sq := &mockSQLFunc{}
			funcs := sq.BuildFuncMap()

			tmpl, err := template.New("test").Delims(cfg.LeftDelim, cfg.RightDelim).Funcs(funcs).Parse(preprocessed)
			if err != nil {
				t.Fatalf("Parse error: %v\nSQL: %s\nPreprocessed: %s", err, tc.sql, preprocessed)
			}

			var sb strings.Builder
			err = tmpl.Execute(&sb, tc.params)
			if err != nil {
				t.Fatalf("Execute error: %v\nSQL: %s\nPreprocessed: %s", err, tc.sql, preprocessed)
			}

			gotSQL := sb.String()
			if gotSQL != tc.wantSQL {
				t.Errorf("SQL: got %q, want %q\n  preprocessed: %s", gotSQL, tc.wantSQL, preprocessed)
			}
			if len(sq.Args) != len(tc.wantArgs) {
				t.Errorf("Args count: got %v (%d), want %v (%d)", sq.Args, len(sq.Args), tc.wantArgs, len(tc.wantArgs))
			} else {
				for i := range sq.Args {
					if sq.Args[i] != tc.wantArgs[i] {
						t.Errorf("Args[%d]: got %v, want %v", i, sq.Args[i], tc.wantArgs[i])
					}
				}
			}
		})
	}
}

func TestCustomColumnToFieldName(t *testing.T) {
	// 使用 SnakeToPascalCase 转换
	cfg := Config{
		LeftDelim:         "{",
		RightDelim:        "}",
		ColumnToFieldName: SnakeToPascalCase,
	}

	assertPreprocess(t, "{user_name}", "{param .UserName}", cfg)
	assertPreprocess(t, "{@user_name}", "{param .UserName}", cfg)

	// 函数参数也会被转换（只转字段名，不额外 param 包装）
	assertPreprocess(t, "{param id name}", "{param .Id .Name}", cfg)
}

func TestCustomDelim(t *testing.T) {
	cfg := Config{
		LeftDelim:  "{{",
		RightDelim: "}}",
	}

	assertPreprocess(t, "{{id}}", "{{param .id}}", cfg)
	assertPreprocess(t, "{{id, name}}", "{{param .id}}, {{param .name}}", cfg)
}

func TestSnakeToPascalCase(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"id", "Id"},
		{"user_name", "UserName"},
		{"create_time", "CreateTime"},
		{"a_b_c", "ABC"},
		{"", ""},
		{"user", "User"},
	}

	for _, c := range cases {
		got := SnakeToPascalCase(c.input)
		if got != c.want {
			t.Errorf("SnakeToPascalCase(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestIdentityColumnToFieldName(t *testing.T) {
	cases := []string{"id", "user_name", "ID", "UserName", ""}
	for _, c := range cases {
		got := IdentityColumnToFieldName(c)
		if got != c {
			t.Errorf("IdentityColumnToFieldName(%q) = %q, want %q", c, got, c)
		}
	}
}

// 测试 README 中的等价写法
func TestReadmeEquivalentForms(t *testing.T) {
	cfg := DefaultConfig()

	// README 说以下等价：
	form1 := "VALUES ({id}, {user_name}, {age})"
	form2 := "VALUES ({id, user_name, age})"
	form3 := "VALUES ({@id, @user_name, @age})"

	result1 := Preprocess(form1, cfg)
	result2 := Preprocess(form2, cfg)
	result3 := Preprocess(form3, cfg)

	// 所有形式预处理后应该是一样的
	if result1 != result2 {
		t.Errorf("form1 %q != form2 %q", result1, result2)
	}
	if result2 != result3 {
		t.Errorf("form2 %q != form3 %q", result2, result3)
	}

	// 且应该是参数化标准形式
	expected := "VALUES ({param .id}, {param .user_name}, {param .age})"
	if result1 != expected {
		t.Errorf("got %q, want %q", result1, expected)
	}
}

func TestLikeInImplicitConversion(t *testing.T) {
	cfg := DefaultConfig()

	// like ? 和 in ? 走专门的模板函数（安全）
	assertPreprocess(t,
		"SELECT * FROM user WHERE user_name like ?",
		"SELECT * FROM user WHERE user_name {like .user_name}",
		cfg)

	assertPreprocess(t,
		"SELECT * FROM user WHERE user_name LIKE ?",
		"SELECT * FROM user WHERE user_name {like .user_name}",
		cfg)

	assertPreprocess(t,
		"SELECT * FROM user WHERE id in ?",
		"SELECT * FROM user WHERE id {in .id}",
		cfg)

	assertPreprocess(t,
		"SELECT * FROM user WHERE id IN ?",
		"SELECT * FROM user WHERE id {in .id}",
		cfg)

	// 带自定义转换函数
	cfg2 := Config{
		LeftDelim:         "{",
		RightDelim:        "}",
		ColumnToFieldName: SnakeToPascalCase,
	}
	assertPreprocess(t,
		"SELECT * FROM user WHERE user_name like ?",
		"SELECT * FROM user WHERE user_name {like .UserName}",
		cfg2)
}

func TestLikeInNotAffectedInStrings(t *testing.T) {
	cfg := DefaultConfig()

	// 字符串中的 like ? 不应该被转换
	assertPreprocess(t,
		"SELECT 'like ?' FROM user WHERE id = 1",
		"SELECT 'like ?' FROM user WHERE id = 1",
		cfg)

	// 已经在模板内的 like 不应该被双重转换
	assertPreprocess(t,
		"SELECT * FROM user WHERE user_name {like .user_name}",
		"SELECT * FROM user WHERE user_name {like .user_name}",
		cfg)
}

// ============================================================================
// INSERT VALUES(?) 隐式展开测试
// ============================================================================

func TestInsertValuesExpandBasic(t *testing.T) {
	cfg := DefaultConfig()
	cfg2 := Config{LeftDelim: "{", RightDelim: "}", ColumnToFieldName: SnakeToPascalCase}

	// 基本展开：列名 → param 多参数
	assertPreprocess(t,
		"INSERT INTO student (id, name, age) VALUES(?)",
		"INSERT INTO student (id, name, age) VALUES({param .id .name .age})",
		cfg)

	// snake_case → PascalCase 转换
	assertPreprocess(t,
		"INSERT INTO student (id, user_name, age) VALUES(?)",
		"INSERT INTO student (id, user_name, age) VALUES({param .Id .UserName .Age})",
		cfg2)

	// VALUES 周围有空白
	assertPreprocess(t,
		"INSERT INTO t (a, b) VALUES ( ? )",
		"INSERT INTO t (a, b) VALUES ({param .a .b})",
		cfg)

	// 小写
	assertPreprocess(t,
		"insert into t (id, name) values(?)",
		"insert into t (id, name) values({param .id .name})",
		cfg)
}

func TestInsertValuesExpandNoMultiRow(t *testing.T) {
	cfg := DefaultConfig()
	cfg2 := Config{LeftDelim: "{", RightDelim: "}", ColumnToFieldName: SnakeToPascalCase}

	// 多行批量 VALUES(?), (?) — 不可控，不展开
	assertPreprocess(t,
		"INSERT INTO t (a, b) VALUES(?),(?)",
		"INSERT INTO t (a, b) VALUES(?),(?)",
		cfg)

	assertPreprocess(t,
		"INSERT INTO t (a, b) VALUES(?), (?), (?)",
		"INSERT INTO t (a, b) VALUES(?), (?), (?)",
		cfg)

	// 多行 + PascalCase — 同样不展开
	assertPreprocess(t,
		"INSERT INTO t (user_name, email) VALUES(?), (?)",
		"INSERT INTO t (user_name, email) VALUES(?), (?)",
		cfg2)

	// 多行但每行不是单个 ? — 本来就不展开
	assertPreprocess(t,
		"INSERT INTO t (a, b) VALUES(?, ?), (?, ?)",
		"INSERT INTO t (a, b) VALUES(?, ?), (?, ?)",
		cfg)
}

func TestInsertValuesExpandBacktickColumns(t *testing.T) {
	cfg2 := Config{LeftDelim: "{", RightDelim: "}", ColumnToFieldName: SnakeToPascalCase}

	// 反引号表名 + 列名
	assertPreprocess(t,
		"INSERT INTO `student` (`id`, `user_name`, `age`) VALUES(?)",
		"INSERT INTO `student` (`id`, `user_name`, `age`) VALUES({param .Id .UserName .Age})",
		cfg2)

	// 混合
	assertPreprocess(t,
		"INSERT INTO `t` (normal_col, `backtick_col`) VALUES(?)",
		"INSERT INTO `t` (normal_col, `backtick_col`) VALUES({param .NormalCol .BacktickCol})",
		cfg2)
}

func TestInsertValuesExpandSchemaPrefix(t *testing.T) {
	cfg2 := Config{LeftDelim: "{", RightDelim: "}", ColumnToFieldName: SnakeToPascalCase}

	// schema.table(col) — 列名含 . 前缀取最后一段
	assertPreprocess(t,
		"INSERT INTO dbo.student (id, user_name) VALUES(?)",
		"INSERT INTO dbo.student (id, user_name) VALUES({param .Id .UserName})",
		cfg2)
}

// 明确不该展开的场景
func TestInsertValuesExpandNoExpand(t *testing.T) {
	cfg := DefaultConfig()

	// 无列名列表 → 无法展开，保持原样
	assertPreprocess(t,
		"INSERT INTO t VALUES(?)",
		"INSERT INTO t VALUES(?)",
		cfg)

	// 显式多个 ? — 用户自己控制数量，不覆盖
	assertPreprocess(t,
		"INSERT INTO t (a, b) VALUES(?, ?)",
		"INSERT INTO t (a, b) VALUES(?, ?)",
		cfg)

	// 有实际值 — 不是占位符
	assertPreprocess(t,
		"INSERT INTO t (a, b) VALUES(1, 2)",
		"INSERT INTO t (a, b) VALUES(1, 2)",
		cfg)

	// 多行但每行不是单个 ? — 不展开
	assertPreprocess(t,
		"INSERT INTO t (a, b) VALUES(?, ?), (?, ?)",
		"INSERT INTO t (a, b) VALUES(?, ?), (?, ?)",
		cfg)

	// 已有模板语法 — 不重复处理
	assertPreprocess(t,
		"INSERT INTO t (id, name) VALUES ({param .Id .Name})",
		"INSERT INTO t (id, name) VALUES ({param .Id .Name})",
		cfg)
}

func TestInsertValuesExpandDoesNotAffectSelect(t *testing.T) {
	cfg := DefaultConfig()

	// SELECT 子查询中的 VALUES 不是 INSERT 的 VALUES — 不受影响
	assertPreprocess(t,
		"SELECT * FROM t WHERE x = (SELECT MAX(id) FROM other WHERE y = ?)",
		"SELECT * FROM t WHERE x = (SELECT MAX(id) FROM other WHERE y = {param .y})",
		cfg)

	// SELECT 子查询中的 ? — 走 processColumnEqualsQuestion 而不是 INSERT
	assertPreprocess(t,
		"SELECT * FROM t WHERE id = ?",
		"SELECT * FROM t WHERE id = {param .id}",
		cfg)
}

func TestInsertValuesExpandExecute(t *testing.T) {
	cfg := Config{LeftDelim: "{", RightDelim: "}", ColumnToFieldName: SnakeToPascalCase}

	// 用 mock sqlFunc 执行完整链路 — 传 map（key 为 PascalCase 字段名）
	sq := &mockSQLFunc{}
	funcs := sq.BuildFuncMap()

	sql := "INSERT INTO student (id, user_name, age) VALUES(?)"
	preprocessed := Preprocess(sql, cfg)

	tmpl, err := template.New("test").Delims(cfg.LeftDelim, cfg.RightDelim).Funcs(funcs).Parse(preprocessed)
	if err != nil {
		t.Fatalf("Parse error: %v, preprocessed: %s", err, preprocessed)
	}

	var sb strings.Builder
	err = tmpl.Execute(&sb, map[string]any{
		"Id":       int64(1),
		"UserName": "Alice",
		"Age":      25,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	gotSQL := sb.String()
	wantSQL := "INSERT INTO student (id, user_name, age) VALUES(?,?,?)"
	if gotSQL != wantSQL {
		t.Errorf("SQL: got %q, want %q", gotSQL, wantSQL)
	}

	wantArgs := []any{int64(1), "Alice", 25}
	if len(sq.Args) != len(wantArgs) {
		t.Fatalf("Args count: got %d, want %d, got=%v", len(sq.Args), len(wantArgs), sq.Args)
	}
	for i := range sq.Args {
		if sq.Args[i] != wantArgs[i] {
			t.Errorf("Args[%d]: got %v, want %v", i, sq.Args[i], wantArgs[i])
		}
	}

	t.Logf("Preprocessed: %s", preprocessed)
	t.Logf("Final SQL:    %s", gotSQL)
	t.Logf("Args:         %v", sq.Args)
}
