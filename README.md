# tgsql

[![Go Reference](https://pkg.go.dev/badge/github.com/tianxinzizhen/tgsql.svg)](https://pkg.go.dev/github.com/tianxinzizhen/tgsql)

`tgsql` 是一个 Go 语言 SQL 模板库。你可以用类 Go template 的语法在注释里写 SQL，运行时它会自动完成：

- 预处理：把 `{user_name}` 按列名转字段名、`like ?` 自动包 `%value%`、方括号变可选条件
- 模板渲染：用 Go `text/template` 的 `Funcs → Parse` 顺序安全执行
- 结果扫描：把 `*sql.Rows` 里的列自动映射到 struct / slice / map / 单值

仅依赖 `database/sql` 和 `github.com/go-sql-driver/mysql`（MySQL 驱动可选，其他驱动也能用）。

---

## 安装

```bash
go get github.com/tianxinzizhen/tgsql
```

Go 版本：>= 1.23

---

## 快速开始

### 1. 连接数据库 + 创建 TgenSql 实例

```go
package main

import (
    "context"
    "database/sql"
    "embed"
    "fmt"
    "time"

    "github.com/tianxinzizhen/tgsql"
    _ "github.com/go-sql-driver/mysql"
)

func main() {
    db, err := sql.Open("mysql", "user:password@tcp(localhost:3306)/testdb?charset=utf8mb4&parseTime=True&loc=Local")
    if err != nil { panic(err) }
    defer db.Close()

    tdb := tgsql.NewTgenSql(db)

    // 可选：开启 SQL 日志
    tdb.SqlLogFunc(func(ctx context.Context, funcName, sql string, args ...any) {
        fmt.Printf("[%s] %s\n  SQL: %s\n  Args: %v\n",
            time.Now().Format("15:04:05"), funcName, sql, args)
    })

    _ = runDAO(tdb)
}
```

### 2. 在 DAO struct 上写 `//sql` / `/*sql*/` 注解

`user_dao.go`：

```go
package main

import (
    "context"
    "database/sql"
    "embed"
)

// 模型
type User struct {
    ID       int64
    UserName string
    Age      int
}

//go:embed *.go
var userDaoFS embed.FS

// UserDao —— 每个 func 字段的注释就是 SQL 模板
type UserDao struct {
    /*sql
      INSERT INTO user (user_name, age)
      VALUES(?)
    */
    Insert   func(ctx context.Context, u *User) (sql.Result, error)

    //sql SELECT * FROM user WHERE id = {.id}
    GetByID  func(ctx context.Context, id int64) (*User, error)

    //sql SELECT * FROM user WHERE id IN {in .}
    GetByIds func(ctx context.Context, ids []int64) ([]*User, error)

    /*sql
      SELECT * FROM user
      WHERE 1 = 1
      [AND age > {.age}]
      [AND user_name {like .name}]
    */
    List     func(ctx context.Context, age int, name string) ([]*User, error)

    //sql UPDATE user SET {set .} WHERE id = {.Id}
    Update   func(ctx context.Context, u *User) error

    //sql DELETE FROM user WHERE id = {.id}
    Delete   func(ctx context.Context, id int64) error
}
```

### 3. 加载 + 绑定

```go
func runDAO(tdb *tgsql.TgenSql) error {
    // 加载 //sql 注解（三种方式任选一种）
    // tdb.LoadFuncDataInfo(userDaoFS)                  // 从 embed.FS（推荐生产）
    // tdb.LoadFuncDataInfoString("//sql ... 源码字符串") // 从字符串
    // tdb.LoadFuncDataInfoBytes([]byte{...})             // 从字节数组
    if err := tdb.LoadFuncDataInfo(userDaoFS); err != nil {
        return err
    }

    var dao UserDao
    if err := tgsql.InitDBFunc(tdb, &dao); err != nil {
        return err
    }

    ctx := context.Background()

    // 调用 —— 和普通函数一样
    res, err := dao.Insert(ctx, &User{UserName: "张三", Age: 25})
    if err != nil { return err }
    lastID, _ := res.LastInsertId()

    u, err := dao.GetByID(ctx, lastID)
    if err != nil { return err }
    fmt.Printf("用户: %+v\n", u)

    // 可选条件：两个参数都给 → WHERE 1=1 AND age > 20 AND user_name like '%张%'
    users, err := dao.List(ctx, 20, "张")
    if err != nil { return err }
    fmt.Printf("列表: %+v\n", users)

    return nil
}
```

---

## 模板语法

### 参数引用

模板里可以用任意一种风格引用参数，预处理后统一处理：

| 写法 | 说明 |
|---|---|
| `{.field}` | Go struct 字段名（PascalCase），如 `{.UserName}` |
| `{field}` / `{@field}` | 数据库列名风格 → 自动转 PascalCase |
| `{.age}` / `{age}` | → 都变成 `{.Age}` |
| `{user_name}` | → 变成 `{.UserName}` |
| `?` | 原生 `database/sql` 占位符 |
| `列名 = ?` | **自动参数化**：预处理转成 `列名 = {param .列名}`（最简便的等值条件写法） |
| `列名 = @列名` | 同上：`@列名` 风格也被识别，自动转成 `{param .列名}` |

> 默认列名转换：`snake_case → PascalCase`（`DefaultColumnToFieldNameFunc`）。
> 可通过 `tdb.SetColumnToFieldNameFunc(自定义函数)` 覆盖。

### 可选条件：方括号 `[...]`

方括号包住的内容会被自动转成 `{if ...}...{end}`：

```sql
SELECT * FROM user WHERE 1 = 1
[AND age > {.age}]                  -- age 非零时才输出
[AND user_name {like .name}]        -- name 非空时才输出
```

等价于：

```sql
SELECT * FROM user WHERE 1 = 1
{if .Age} AND age > {.Age} {end}
{if .Name} AND user_name {like .Name} {end}
```

---

## 内置模板函数

函数名 → 最终生成的 SQL 片段。所有函数自动 `unwrap` 解 interface{} / 指针。

| 函数 | 生成 | 说明 |
|---|---|---|
| `{like .Name}` | `like ?`，args 加 `"%Name值%"` | 两端通配 |
| `{liker .Name}` | `like ?`，args 加 `"Name值%"` | 右通配 |
| `{likel .Name}` | `like ?`，args 加 `"%Name值"` | 左通配 |
| `{in .Ids}` | `IN (?, ?, ...)` | 自动展开 slice；单个值也兼容 |
| `{set .User}` | `col1=?, col2=?, ...` | 跳过零值字段；字段名自动 Pascal→snake |
| `{set "u" .User}` | `u.col1=?, u.col2=?, ...` | 带表别名 |
| `{where .Filter}` | `col1=? AND col2=? ...` | 同 set，用 `and` 连接 |
| `{param .a .b .c}` | `?, ?, ?` | 多字段批量输出 |
| `{json .Info}` / `{marshal .Info}` | `?`，args 加 JSON 字符串 | struct / map / slice 自动 `json.Marshal` |
| `{comma .Flag}` | `,` 或空串 | 数字字段 >0 时输出逗号（用于动态列列表） |
| `{sql "en"}` | `en` 原样输出 | 字符串拼接 |

### `set` / `where` 的字段自动过滤

```go
type UpdateUser struct {
    ID       int64   // 有值 → 包含
    UserName string  // ""   → 跳过
    Age      int     // 0    → 跳过
    Email    string  // "x@x"→ 包含
}
```

```sql
UPDATE user SET {set .} WHERE id = {.Id}
-- 生成: UPDATE user SET id=?, email=? WHERE id = ?
```

### 隐式转换（预处理自动做）

SQL 里写了就能用——预处理帮你把这些简便写法转成模板调用：

| 模板里写 | 自动转成 | 说明 |
|---|---|---|
| `列名 = ?` | `列名 = {param .列名}` | 最常用！等值条件自动参数化 |
| `列名 = @列名` | `列名 = {param .列名}` | `@列名` 是 Go `database/sql` 的原生命名参数风格，预处理也支持 |
| `@列名`（裸） | `{param .列名}` | 单独的 `@列名` 也会被识别 |
| `列名 like ?` | `列名 {like .列名}` | `like` 是参数敏感函数——自动套 `%value%` |
| `列名 in ?` | `列名 {in .列名}` | `in` 是参数敏感函数——自动展开 `(?, ?, ...)` |
| `列名 {like .列名}` | 保持不变（不重复生成 like） | 直接写函数调用也支持 |
| `列名 = {user_name}` | `列名 = {.UserName}`（列名→字段名） | 预处理自动做 snake_case → PascalCase 转换 |

**重点示例：**

```sql
-- 用户写：
SELECT * FROM user WHERE id = ? AND user_name like ? AND age = ?

-- 预处理自动转成：
SELECT * FROM user WHERE id = {param .id} AND user_name {like .user_name} AND age = {param .age}

-- 最终执行等价于：
SELECT * FROM user WHERE id = ? AND user_name like ? AND age = ?
-- args = [1, "%Alice%", 25]
```

```sql
-- 用户写（@列名 风格）：
SELECT * FROM user WHERE id = @id AND name = @name

-- 自动转成：
SELECT * FROM user WHERE id = {param .id} AND name = {param .name}
```

---

## `?option{...}` 指令

在 SQL 开头用 `?option{}` 给这条语句加行为开关：

```sql
//sql?option{not_prepare:true, name:CustomGet} SELECT * FROM user WHERE id = {.id}
```

| 选项 | 默认 | 说明 |
|---|---|---|
| `not_prepare` | `false` | 跳过 `sql.Prepare`，直接把所有 `?` 插值进 SQL 再执行。适合 DDL / 带自定义 SQL 片段的查询 |
| `batch_insert` | `false` | 批量 INSERT，参数必须是 slice；每条记录用同一模板渲染后单独 `ExecContext`，相同 SQL 复用 `*sql.Stmt` |
| `name` | 字段名 | 覆盖 `Get` / `Insert` 这类自动生成的字段名 |

### 批量 INSERT 示例

```go
type BatchDao struct {
    //sql?option{batch_insert:true} INSERT INTO user (user_name, age) VALUES(?)
    BatchInsert func(ctx context.Context, rows []*User) error
}
```

调用 `dao.BatchInsert(ctx, []*User{{...}, {...}, {...}})` 后，框架内部会循环 3 次：

```sql
INSERT INTO user (user_name, age) VALUES(?)  args=[张三 20]
INSERT INTO user (user_name, age) VALUES(?)  args=[李四 25]
INSERT INTO user (user_name, age) VALUES(?)  args=[王五 30]
```

相同 SQL 只 `Prepare` 一次，然后复用 `*sql.Stmt` 执行。

---

## 自定义分隔符

默认 `{` `}`，可以改成任意字符（Go template 允许的）：

```go
tdb.Delims("{{", "}}")
```

---

## 自定义模板函数

### 基本用法

```go
tdb.AddTemplateFunc("myquote", func(s string) string {
    return "`" + s + "`"
})

// 或批量注册
tdb.AddAllTemplateFunc(template.FuncMap{
    "geo": func(lat, lng float64) string { return fmt.Sprintf("POINT(%f %f)", lng, lat) },
})
```

### 带 `ArgsCollector` 的自定义函数（追加 SQL 参数）

当自定义函数需要把值追加到 SQL `args` 列表时，让第一个参数声明为 `ArgsCollector` 类型：

```go
tdb.AddTemplateFunc("pt", func(collect tgsql.ArgsCollector, lng, lat float64) string {
    collect(lng, lat)                 // 自动追加到当前 SQL args
    return "ST_GeomFromText(POINT(? ?))"
})
```

模板里正常调用：

```sql
SELECT ST_X(g) FROM geo_points WHERE location = {pt .Lng .Lat}
```

预处理后：`{pt .Lng .Lat}`（用户函数名不会被隐式展开成 `{param .pt}`）

执行时：

```
SQL: SELECT ST_X(g) FROM geo_points WHERE location = ST_GeomFromText(POINT(? ?))
args: [116.4074, 39.9042]
```

**关键点：**
- `ArgsCollector` **不会出现在模板签名里**——模板只用写 `{pt .Lng .Lat}`，collector 在内部自动注入
- 内置函数名（`like` / `in` / `param` / `set` / `where` ...）**优先于**用户注册的同名函数
- 两种函数签名都可以（纯函数或带 collector）：框架会自动检测并适配

### 函数签名对照表

| 注册 | 模板调用 | SQL 渲染 | args 追加 |
|---|---|---|---|
| `func(s string) string` | `{hello .Name}` | `[Alice]` | 无 |
| `func(ArgsCollector, float64, float64) string` | `{pt .Lng .Lat}` | `POINT(? ?)` | `[lng, lat]` |
| `func(ArgsCollector, ...string) string` | ⚠️ 不支持 variadic 的 collector 风格 | | |
| 用户注册 `func(like string) string` | `{like .Name}` | 仍然是内置 like 的结果（内置优先） | |
```

---

## 直接调用 `BuildSQL`（不走 InitDBFunc）

如果你不想定义 DAO struct，也可以直接渲染模板：

```go
sqlStr, args, err := tdb.BuildSQL(ctx,
    `SELECT * FROM user WHERE 1=1 [AND age > {.age}] [AND user_name {like .name}]`,
    map[string]any{"Age": 20, "Name": "张"},
)
// sqlStr = "SELECT * FROM user WHERE 1=1 AND age > ? AND user_name like ?"
// args   = [20, "%张%"]
```

---

## 高级特性

### 注册参数转换器（写入 DB 前）

当某个自定义类型无法被 `database/sql` 直接序列化时，实现 `sqlval.Convert[T]`：

```go
// 业务用的地理坐标类型
type GeoPoint struct {
    Lng float64
    Lat float64
}

// 实现 Convert[T]：把 GeoPoint → MySQL POINT 的 25 字节 WKB
type GeoConverter struct{}

func (g *GeoConverter) ConvertValue(v GeoPoint) (any, error) {
    // SRID(4B) + order(1B=1) + POINTtype(4B=1) + X(8B big-endian) + Y(8B big-endian)
    return nil, nil // 示例：实际实现见 test/geo_test.go
}
func (g *GeoConverter) ConvertValuePtr(v *GeoPoint) (any, error) {
    return g.ConvertValue(*v)
}

// 注册
sqlval.RegisterConvert[GeoPoint](&GeoConverter{})
```

接口定义（go 1.18+ 泛型）：

```go
type Convert[T any] interface {
    ConvertValue(v T)     (any, error)
    ConvertValuePtr(v *T) (any, error)
}
```

### 注册结果扫描器（读 DB 后）

当查询结果需要转成自定义类型时，实现 `sqlval.ScanVal[T]`（同时要满足 `sql.Scanner`）：

```go
type GeoScanner struct {
    bytes []byte
}

// sql.Scanner —— 用指针接收者
func (s *GeoScanner) Scan(dest any) error {
    // dest 是 mysql 驱动传过来的 []byte WKB
    switch v := dest.(type) {
    case []byte:
        s.bytes = v
    case string:
        s.bytes = []byte(v)
    }
    return nil
}

// ScanVal[T] —— 用值接收者
func (s GeoScanner) ScanValue() (GeoPoint, error) {
    // 解析 WKB → GeoPoint
    return GeoPoint{Lng: 116.4, Lat: 39.9}, nil
}
func (s GeoScanner) ScanValuePtr() (*GeoPoint, error) {
    p, _ := s.ScanValue()
    return &p, nil
}

// 注册
sqlval.RegisterScanVal[GeoPoint](GeoScanner{})
```

接口定义：

```go
type ScanVal[T any] interface {
    ScanValue()     (T, error)
    ScanValuePtr()  (*T, error)
}
```

然后 DAO struct 里直接用这个类型，框架扫描时会自动走注册的转换器：

```go
type GeoDao struct {
    //sql SELECT * FROM geo_points WHERE id = {.id}
    Get func(ctx context.Context, id int64) (*GeoPoint, error)
}
```

---

## `InitDBFunc` 自动推断的操作类型

根据返回值签名自动分发到不同路径：

| 返回值签名 | 操作类型 | 内部行为 |
|---|---|---|
| `(sql.Result, error)` | `execAction` | `db.ExecContext` |
| `(error)` | `execNoResultAction` | `db.ExecContext` / batch insert / `option{not_prepare}` |
| `([]*T, error)` | `selectAction` | `db.QueryContext` + 扫描所有行 |
| `(*T, error)` | `selectOneAction` | `db.QueryContext` + 扫描第一行后 break |
| `(error)` + `option{batch_insert:true}` | 批量路径 | 遍历 slice 参数，逐条 `ExecContext` |

---

## 项目结构

```
tgsql/
├── tgensql.go                 TgenSql 主入口、NewTgenSql、BuildSQL、Execute
│                              导出类型 ArgsCollector，buildFuncMapForExecution 合并用户函数
├── sql_func.go                内置模板函数（like/in/set/where/param/marshal...）
├── makefunc_context.go        InitDBFunc 核心：reflect.MakeFunc 把 //sql 字段填充成闭包
├── sql_option.go              funcExecOption 内部选项（param、sql、args、result）
├── sql_common_type.go         Operation 枚举、contextType/errorType/sqlResultType
├── sql_error.go / sql_recover.go / sql_tx.go / sql_record.go
├── template/pre_parse/       预处理引擎
│   ├── pre_sql.go             {col}→{.Col}、方括号→{if}、隐式转换、IsFunc 可插拔
│   ├── sql_lex.go             SQL 词法扫描（跳过引号内的内容）
│   └── pre_sql_test.go        preprocessor 单测
├── load/                      从 Go 源码里提取 //sql 注解
│   ├── all_load.go            LoadFuncDataInfo / LoadFuncDataInfoString / LoadFuncDataInfoBytes
│   ├── sql_comment_data.go    parseComment → SqlDataInfo{TypeName,Name,FuncName,Sql,Param,...}
│   └── load_test.go           extractPkgPath / extractSQLBody / applyOption 单测
├── sqlval/                    参数转换器 & 结果扫描器
│   ├── convert_value.go       Convert[T] 接口 + localConvertVal
│   ├── result_scan.go         GetScanDest：columns → dest[] + deferFn[]
│   ├── scan.go                ScanVal[T] 接口 + json 列识别
│   └── scan_json.go           注册为 ScanVal 的 json.RawMessage 包装
├── util/
│   ├── fieldName.go           SnakeToCamel / CamelToPascal / PascalToSnakeCase
│   ├── reflect.go             Indirect 等 reflect 工具
│   └── sqlescape.go           InterpolateParams（option{not_prepare} 用）
└── test/                      集成测试（需要 MySQL 数据库）
    ├── base.go                newTDB / testDB / Student model / DSN
    ├── student_dao.go         StudentDao 真实 DAO
    ├── student_dao_test.go    CRUD + 可选条件
    ├── query_test.go          BuildSQL 直接调用
    ├── geo_test.go            GeoPoint 参数转换器 + 结果扫描器
    ├── batch_insert_test.go   option{batch_insert:true}
    └── custom_func_test.go    自定义模板函数 + ArgsCollector（7 个用例）
```

---

## 运行测试

需要本地有 MySQL（默认 `root:Lix@1234@tcp(localhost:3306)/tgsql_test`）：

```bash
# 先建表 + 种子数据
mysql -uroot -p'Lix@1234' -e "
CREATE DATABASE IF NOT EXISTS tgsql_test CHARSET utf8mb4;
USE tgsql_test;
DROP TABLE IF EXISTS student;
CREATE TABLE student (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_name   VARCHAR(50) NOT NULL,
    age         INT,
    email       VARCHAR(100),
    create_time DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO student (id, user_name, age) VALUES
    (1, 'Alice', 20), (2, 'Bob', 25), (3, 'Carol', 30), (4, 'David', 35), (5, 'Eve', 28);
"

# 跑测试
go test ./... -count=1

# 带 race detector
go test -race ./... -count=1
```

仅跑不依赖 DB 的包：

```bash
go test ./load/ ./template/pre_parse/ -v -count=1
```

---

## 许可证

MIT License
