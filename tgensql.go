package tgsql

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"text/template"

	"github.com/tianxinzizhen/tgsql/load"
	"github.com/tianxinzizhen/tgsql/sqlval"
	preparse "github.com/tianxinzizhen/tgsql/template/pre_parse"
	"github.com/tianxinzizhen/tgsql/util"
)

type TgenSql struct {
	db                      *sql.DB
	localFuncDataInfo       *load.LoadFuncDataInfo
	columnToFieldNameFunc   func(column string) string
	fieldNameToColumnFunc   func(fieldName string) string
	leftDelim, rightDelim   string
	sqlLogFunc              func(ctx context.Context, funcName, sql string, args ...any)
	sqlFunc                 template.FuncMap
	template                map[uintptr]map[int]*template.Template
	SqlEscapeBytesBackslash bool
}

func DefaultColumnToFieldNameFunc(column string) string {
	// 默认把数据库列名转换成驼峰命名法
	fieldName := util.SnakeToCamel(column)
	return util.CamelToPascal(fieldName)
}
func (tdb *TgenSql) SetColumnToFieldNameFunc(columnToFieldNameFunc func(column string) string) {
	tdb.columnToFieldNameFunc = columnToFieldNameFunc
}

// SetFieldNameToColumnFunc 设置 struct 字段名 → 数据库列名的转换函数（PascalCase → snake_case）
// 默认使用 util.PascalToSnakeCase
func (tdb *TgenSql) SetFieldNameToColumnFunc(fieldNameToColumnFunc func(fieldName string) string) {
	tdb.fieldNameToColumnFunc = fieldNameToColumnFunc
}

func (tdb *TgenSql) SetSqlEscapeBytesBackslash(sqlEscapeBytesBackslash bool) {
	tdb.SqlEscapeBytesBackslash = sqlEscapeBytesBackslash
}

func (tdb *TgenSql) Delims(leftDelim, rightDelim string) {
	tdb.leftDelim = leftDelim
	tdb.rightDelim = rightDelim
}

func (tdb *TgenSql) SqlLogFunc(sqlLogFunc func(ctx context.Context, funcName, sql string, args ...any)) {
	tdb.sqlLogFunc = sqlLogFunc
}

func (tdb *TgenSql) AddTemplateFunc(key string, funcMethod any) {
	tdb.sqlFunc[key] = funcMethod
}

func (tdb *TgenSql) AddAllTemplateFunc(sqlFunc template.FuncMap) {
	for k, v := range sqlFunc {
		tdb.sqlFunc[k] = v
	}
}

func (tdb *TgenSql) LoadFuncDataInfo(dbFuncData embed.FS) error {
	return tdb.localFuncDataInfo.LoadFuncDataInfo(dbFuncData)
}

func (tdb *TgenSql) LoadFuncDataInfoBytes(dbFuncData []byte) error {
	return tdb.localFuncDataInfo.LoadFuncDataInfoBytes(dbFuncData)
}

func (tdb *TgenSql) LoadFuncDataInfoString(dbFuncData string) error {
	return tdb.localFuncDataInfo.LoadFuncDataInfoString(dbFuncData)
}

func NewTgenSql(sqlDB *sql.DB) *TgenSql {
	tdb := &TgenSql{
		db:                    sqlDB,
		leftDelim:             "{",
		rightDelim:            "}",
		columnToFieldNameFunc: DefaultColumnToFieldNameFunc,
		fieldNameToColumnFunc: util.PascalToSnakeCase,
		sqlFunc:               make(template.FuncMap),
		localFuncDataInfo:     load.NewLoadFuncDataInfo(),
	}
	// 注册默认模板函数（Parse 阶段需要函数存在）
	defaultFuncs := (&sqlFunc{fieldNameToColumnFunc: tdb.fieldNameToColumnFunc}).BuildFuncMap()
	for k, v := range defaultFuncs {
		tdb.sqlFunc[k] = v
	}
	return tdb
}

const (
	optionNone int = 1 << iota
	optionNotPrepare
	optionBatchInsert
)

// preprocessConfig 返回当前 TgenSql 的预处理配置
// 消除 ParseSql / BuildSQL / InitDBFunc 三处重复构造
func (tdb *TgenSql) preprocessConfig() preparse.Config {
	return preparse.Config{
		LeftDelim:         tdb.leftDelim,
		RightDelim:        tdb.rightDelim,
		ColumnToFieldName: tdb.columnToFieldNameFunc,
	}
}

// ParseSql 预处理 SQL 模板 + 创建已带 Funcs 的 Go template
// Funcs 必须在 Parse 之前，这样 Parse 时就能识别模板函数
func (tdb *TgenSql) ParseSql(tsql string) (*template.Template, error) {
	preprocessedSQL := preparse.Preprocess(tsql, tdb.preprocessConfig())

	// 每次 Parse 都用新的 sqlFunc 实例，避免 Args 污染
	sf := &sqlFunc{fieldNameToColumnFunc: tdb.fieldNameToColumnFunc}

	return template.New("").Delims(tdb.leftDelim, tdb.rightDelim).
		Funcs(template.FuncMap(sf.BuildFuncMap())).
		Parse(preprocessedSQL)
}

// executeTemplate 模板执行统一入口
// 内部做 preprocess + 独立 sqlFunc 实例 + 正确的 Funcs/Parse 顺序
// 根治了 Funcs-after-Parse bug
func (tdb *TgenSql) executeTemplate(ctx context.Context, rawSQL string, parms any, funcName string) (string, []any, error) {
	preprocessedSQL := preparse.Preprocess(rawSQL, tdb.preprocessConfig())

	sf := &sqlFunc{fieldNameToColumnFunc: tdb.fieldNameToColumnFunc}
	tpl, err := template.New("").
		Delims(tdb.leftDelim, tdb.rightDelim).
		Funcs(template.FuncMap(sf.BuildFuncMap())).
		Parse(preprocessedSQL)
	if err != nil {
		return "", nil, err
	}

	sb := &strings.Builder{}
	if err := tpl.Execute(sb, parms); err != nil {
		return "", nil, err
	}

	tdb.sqlPrintAndRecord(ctx, funcName, sb.String(), sf.Args)
	return sb.String(), sf.Args, nil
}

// templateBuild — 旧的内部接口（被 InitDBFunc 的 action 调用）
// 已修复：Execute 前先 Parse + Funcs（根治 Funcs-after-Parse bug）
func (tdb *TgenSql) templateBuild(templateSql *template.Template, op *funcExecOption) error {
	pc, _, line, _ := runtime.Caller(2)
	funcName := fmt.Sprintf("%s:%d", runtime.FuncForPC(pc).Name(), line)

	// 从已 Parse 的模板里取回原始 SQL（Go template 不暴露，用 Clone 重新 Parse 更安全）
	// 这里做一个折中：用 Clone + Funcs 然后 Execute（Clone 后的模板允许 Funcs 在 Execute 前）
	sf := &sqlFunc{fieldNameToColumnFunc: tdb.fieldNameToColumnFunc}
	clone, err := templateSql.Clone()
	if err != nil {
		return err
	}
	clone.Funcs(template.FuncMap(sf.BuildFuncMap()))

	sb := &strings.Builder{}
	if err := clone.Execute(sb, op.param); err != nil {
		return err
	}

	sql := sb.String()
	if op.option&optionNotPrepare != 0 {
		op.sql, err = util.InterpolateParams(sql, sf.Args, tdb.SqlEscapeBytesBackslash)
		if err != nil {
			return err
		}
		op.args = nil
	} else {
		op.sql = sql
		op.args = sf.Args
	}
	tdb.sqlPrintAndRecord(op.ctx, funcName, op.sql, op.args)
	return nil
}

// BuildSQL 公开方法：原始 SQL 模板 + 参数 → (最终 SQL, 参数列表, error)
// 内部自动预处理 + 独立 sqlFunc 实例保证 Args 安全 + Funcs-before-Parse 保证正确
func (tdb *TgenSql) BuildSQL(ctx context.Context, tsql string, parms any) (string, []any, error) {
	return tdb.executeTemplate(ctx, tsql, parms, "BuildSQL")
}
func (tdb *TgenSql) query(op *funcExecOption) error {
	return tdb.queryOption(op, queryOption{})
}

type queryOption struct {
	selectOne bool
}

func (tdb *TgenSql) queryOption(op *funcExecOption, queryOption queryOption) error {
	if op.ctx == nil {
		op.ctx = context.Background()
	}
	db := op.GetDB(op.ctx).(sqlDB)
	var err error
	op.args, err = sqlval.ConvertValues(op.db, op.args)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(op.ctx, op.sql, op.args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	for rows.Next() {
		dest, df, err := sqlval.GetScanDest(columns, op.result, tdb.columnToFieldNameFunc)
		if err != nil {
			return err
		}
		err = rows.Scan(dest...)
		if err != nil {
			return err
		}
		for _, fn := range df {
			fn()
		}
		if queryOption.selectOne {
			break
		}
	}
	return nil
}

func (tdb *TgenSql) exec(op *funcExecOption) (ret sql.Result, err error) {
	if op.ctx == nil {
		op.ctx = context.Background()
	}
	switch db := op.GetDB(op.ctx).(type) {
	case sqlDB:
		op.args, err = sqlval.ConvertValues(op.db, op.args)
		if err != nil {
			return nil, err
		}
		result, err := db.ExecContext(op.ctx, op.sql, op.args...)
		if err != nil {
			return nil, err
		}
		return result, nil
	case sqlStmt:
		result, err := db.ExecContext(op.ctx, op.args...)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, errors.New("db not support exec")
}

func (tdb *TgenSql) prepareContext(op *funcExecOption) (ret *sql.Stmt, err error) {
	if op.ctx == nil {
		op.ctx = context.Background()
	}
	if db, ok := op.GetDB(op.ctx).(sqlPrepare); ok {
		result, err := db.PrepareContext(op.ctx, op.sql)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, errors.New("db not support prepare")
}

func (tdb *TgenSql) sqlPrintAndRecord(ctx context.Context, funcName, sql string, args []any) {
	if tdb.sqlLogFunc == nil {
		return
	}
	tdb.sqlLogFunc(ctx, funcName, sql, args...)
	if recordSql, ok := tdb.FromRecordSql(ctx); ok {
		recordSql.List = append(recordSql.List, RecordSqlItem{Sql: sql, Args: args})
	}
}
