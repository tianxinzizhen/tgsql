package tgsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"text/template"

	"github.com/tianxinzizhen/tgsql/load"
	preparse "github.com/tianxinzizhen/tgsql/template/pre_parse"
)

// ============ 参数处理 ============

// handleParam 把 reflect 调用参数分类放进 funcExecOption：
//   - 第一个 context.Context 参数存进 op.ctx
//   - 如果只有一个非 context 的复合参数（map/slice/struct），直接存 op.param
//   - 如果有多个非 context 参数，按 sqlInfo.Param 名字建 paramMap（用 ColumnToFieldName 转 PascalCase）
func handleParam(sqlInfo *load.SqlDataInfo, op *funcExecOption, args []reflect.Value) {
	var opArgs []any
	var useMultiParam bool

	for _, v := range args {
		val := v.Interface()
		opArgs = append(opArgs, val)
		if v.Type().Implements(contextType) {
			if val != nil {
				op.ctx = val.(context.Context)
			}
			continue
		}
		// 非 context 参数
		pvt := v.Type()
		if pvt.Kind() == reflect.Pointer {
			pvt = pvt.Elem()
		}
		switch pvt.Kind() {
		case reflect.Map, reflect.Slice, reflect.Struct:
			if op.param == nil {
				op.param = val
			} else {
				useMultiParam = true
			}
		default:
			useMultiParam = true
		}
	}

	if useMultiParam && len(sqlInfo.Param) > 0 {
		paramMap := make(map[string]any, len(sqlInfo.Param))
		for i, name := range sqlInfo.Param {
			if args[i].Type().Implements(contextType) {
				continue
			}
			key := name
			if op.columnToFieldNameFunc != nil {
				key = op.columnToFieldNameFunc(name)
			}
			paramMap[key] = opArgs[i]
		}
		op.param = paramMap
	}
}

// ============ InitDBFunc 核心 ============

// determineAction 根据函数签名自动推断操作类型
func determineAction(fct reflect.Type) Operation {
	if fct.NumOut() == 0 {
		return execNoResultAction
	}
	firstOut := fct.Out(0)
	switch {
	case firstOut == sqlResultType:
		return execAction
	case firstOut.Implements(errorType):
		return execNoResultAction
	case firstOut.Kind() == reflect.Slice:
		return selectAction
	default:
		return selectOneAction
	}
}

// validateFuncSignature 校验函数参数/返回值类型是否被支持
func validateFuncSignature(fct reflect.Type) error {
	for i := 0; i < fct.NumIn(); i++ {
		t := fct.In(i)
		if t.Implements(contextType) {
			continue
		}
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Func, reflect.Chan:
			return fmt.Errorf("func arg[%d] type not support %s", i, t.Kind())
		}
	}
	for i := 0; i < fct.NumOut(); i++ {
		t := fct.Out(i)
		if t.Implements(errorType) {
			continue
		}
		switch t.Kind() {
		case reflect.Func, reflect.Chan:
			return fmt.Errorf("func return[%d] type not support %s", i, t.Kind())
		case reflect.Interface:
			if !t.Implements(sqlResultType) {
				return fmt.Errorf("func return[%d] interface type not support", i)
			}
		}
	}
	return nil
}

// buildSQLTemplate 预解析 + 构建 *template.Template（InitDBFunc 路径专用）
func buildSQLTemplate(dtName, leftDelim, rightDelim string, columnToFieldName func(string) string, sqlFunc template.FuncMap, rawSQL string) (*template.Template, error) {
	preprocessCfg := preparse.Config{
		LeftDelim:         leftDelim,
		RightDelim:        rightDelim,
		ColumnToFieldName: columnToFieldName,
	}
	preprocessedSQL := preparse.Preprocess(rawSQL, preprocessCfg)
	tpl := template.New(dtName).Delims(leftDelim, rightDelim).Funcs(sqlFunc)
	if _, err := tpl.Parse(preprocessedSQL); err != nil {
		return nil, err
	}
	return tpl, nil
}

// makeDBFuncContext 为一个 //sql 字段生成 reflect.MakeFunc 闭包
func makeDBFuncContext(fct reflect.Type, tdb *TgenSql, action Operation, templateSql *template.Template, sqlInfo *load.SqlDataInfo) reflect.Value {
	return reflect.MakeFunc(fct, func(args []reflect.Value) (results []reflect.Value) {
		var hasReturnErr bool
		if fct.NumOut() > 0 {
			hasReturnErr = fct.Out(fct.NumOut() - 1).Implements(errorType)
		}

		results = make([]reflect.Value, fct.NumOut())
		for i := range results {
			results[i] = reflect.Zero(fct.Out(i))
		}

		op := &funcExecOption{
			ctx:                   context.Background(),
			columnToFieldNameFunc: tdb.columnToFieldNameFunc,
		}
		handleParam(sqlInfo, op, args)

		// 连接复用（Tx 场景下不从 pool 新拿）
		if !GetEnableSqlTx(op.ctx) {
			conn, err := tdb.db.Conn(op.ctx)
			if err != nil {
				setFuncErr(results, hasReturnErr, sqlInfo.FuncName, err)
				return results
			}
			defer conn.Close()
			op.db = conn
		}

		if sqlInfo.NotPrepare {
			op.option |= optionNotPrepare
		}
		if sqlInfo.BatchInsert {
			op.option |= optionBatchInsert
		}

		// 模板构建（批量场景在 changeOp 内部重复调用）
		if op.option&optionBatchInsert == 0 {
			if err := tdb.templateBuild(templateSql, op); err != nil {
				setFuncErr(results, hasReturnErr, sqlInfo.FuncName, err)
				return results
			}
		}

		err := dispatchAction(tdb, op, action, results, hasReturnErr, templateSql, sqlInfo)
		if err != nil {
			setFuncErr(results, hasReturnErr, sqlInfo.FuncName, err)
		}
		return results
	})
}

// dispatchAction 根据 Operation 分发到对应的执行逻辑
func dispatchAction(tdb *TgenSql, op *funcExecOption, action Operation, results []reflect.Value, hasReturnErr bool, tpl *template.Template, sqlInfo *load.SqlDataInfo) error {
	switch action {
	case execAction:
		return runExec(tdb, op, results)
	case selectAction:
		return runQuery(tdb, op, results, hasReturnErr, false)
	case selectOneAction:
		return runQuery(tdb, op, results, hasReturnErr, true)
	case execNoResultAction:
		if op.option&optionBatchInsert != 0 {
			return runBatchInsert(tdb, op, tpl, sqlInfo)
		}
		_, err := tdb.exec(op)
		return err
	default:
		return fmt.Errorf("unknown action: %v", action)
	}
}

// setFuncErr 把 err 写回 results 的最后一个 error 返回值（如果有），否则 panic
func setFuncErr(results []reflect.Value, hasReturnErr bool, funcName string, err error) {
	if hasReturnErr {
		last := len(results) - 1
		results[last] = reflect.ValueOf(funcErr(funcName, err))
	} else {
		panic(recoverLog(err))
	}
}

// runExec 执行写操作（INSERT/UPDATE/DELETE），把 sql.Result 填回 results
func runExec(tdb *TgenSql, op *funcExecOption, results []reflect.Value) error {
	ret, err := tdb.exec(op)
	if err != nil {
		return err
	}
	if ret == nil {
		return nil
	}
	resultVal := reflect.ValueOf(ret)
	for i := range results {
		if results[i].Type() == sqlResultType {
			results[i] = resultVal
		}
	}
	return nil
}

// runQuery 执行 SELECT 查询，把扫描结果填回 results
func runQuery(tdb *TgenSql, op *funcExecOption, results []reflect.Value, hasReturnErr, selectOne bool) error {
	op.result = results
	if hasReturnErr {
		op.result = results[:len(results)-1]
	}
	if selectOne {
		return tdb.queryOption(op, queryOption{selectOne: true})
	}
	return tdb.query(op)
}

// runBatchInsert 批量 INSERT：遍历 slice 参数，为每个元素模板渲染后执行
// 用 stmtMap 缓存已 Prepare 的语句，相同 SQL 只 Prepare 一次
func runBatchInsert(tdb *TgenSql, op *funcExecOption, tpl *template.Template, sqlInfo *load.SqlDataInfo) error {
	if !sqlInfo.BatchInsert {
		return nil
	}
	if !reflect.ValueOf(op.param).IsValid() {
		return errors.New("batch insert param is nil")
	}

	pv := reflect.ValueOf(op.param)
	if pv.Kind() != reflect.Slice {
		return fmt.Errorf("batch insert param must be slice, got %s", pv.Kind())
	}
	if pv.Len() == 0 {
		return errors.New("batch insert param is empty")
	}

	stmtMap := make(map[string]*sql.Stmt)
	defer func() {
		for _, s := range stmtMap {
			if s != nil {
				s.Close()
			}
		}
	}()

	for i := 0; i < pv.Len(); i++ {
		op.param = pv.Index(i).Interface()
		if err := tdb.templateBuild(tpl, op); err != nil {
			return fmt.Errorf("batch[%d] templateBuild: %w", i, err)
		}
		stmt, ok := stmtMap[op.sql]
		if !ok {
			s, err := tdb.prepareContext(op)
			if err != nil {
				return fmt.Errorf("batch[%d] prepare: %w", i, err)
			}
			stmtMap[op.sql] = s
			stmt = s
		}
		_, err := stmt.ExecContext(op.ctx, op.args...)
		if err != nil {
			return fmt.Errorf("batch[%d] exec: %w", i, err)
		}
	}
	return nil
}

// ============ InitDBFunc ============

// InitDBFunc 把 //sql 注解的函数绑定到 dest 结构体上
func InitDBFunc(tdb *TgenSql, dest any) error {
	dv := reflect.ValueOf(dest)
	for dv.Kind() == reflect.Pointer {
		dv = dv.Elem()
	}
	if !dv.IsValid() {
		return errors.New("InitDBFunc in(dest) is not valid")
	}
	dt := dv.Type()
	fkey := fmt.Sprintf("%s.%s", dt.PkgPath(), dt.Name())

	sqlInfos := tdb.localFuncDataInfo.GetSqlDataInfo(fkey)
	if len(sqlInfos) == 0 {
		return fmt.Errorf("InitDBFunc: no sql info for type %s", dt.Name())
	}

	for _, sqlInfo := range sqlInfos {
		fc, ok := dt.FieldByName(sqlInfo.Name)
		if !ok {
			continue // struct 字段被删除，跳过（不报错，方便灰度）
		}
		if fc.Type.Kind() != reflect.Func {
			continue
		}

		// 校验函数签名
		if err := validateFuncSignature(fc.Type); err != nil {
			return fmt.Errorf("%s.%s: %w", dt.Name(), sqlInfo.Name, err)
		}

		// 构建模板
		tpl, err := buildSQLTemplate(dt.Name(), tdb.leftDelim, tdb.rightDelim, tdb.columnToFieldNameFunc, tdb.sqlFunc, sqlInfo.Sql)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", dt.Name(), sqlInfo.Name, err)
		}

		action := determineAction(fc.Type)

		fcv := dv.FieldByIndex(fc.Index)
		fcv.Set(makeDBFuncContext(fc.Type, tdb, action, tpl, sqlInfo))
	}

	if err := checkAllDBFuncSet(tdb, dv); err != nil {
		return err
	}
	return nil
}

// ============ 辅助 ============

func checkAllDBFuncSet(tdb *TgenSql, dv reflect.Value) error {
	for dv.Kind() == reflect.Pointer {
		dv = dv.Elem()
	}
	if !dv.IsValid() {
		return errors.New("checkAllDBFuncSet: struct is not valid")
	}
	dt := dv.Type()
	tgsv := reflect.ValueOf(tdb)
	for i := 0; i < dv.NumField(); i++ {
		f := dv.Field(i)
		ft := f.Type()
		switch {
		case ft.Kind() == reflect.Func && f.IsNil():
			return fmt.Errorf("%s.%s: func field has no sql statement", dt.Name(), dt.Field(i).Name)
		case ft == tgsv.Type() && f.CanSet():
			f.Set(tgsv)
		}
	}
	return nil
}
