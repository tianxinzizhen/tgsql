package sqlval

import (
	"database/sql"
	"fmt"
	"reflect"
)

// getTempScanDest 为不匹配 struct 字段的列创建临时扫描目标（返回 *T 指针）。
// 每次调用返回全新实例，多 goroutine 并发安全。
func getTempScanDest(scanType reflect.Type) any {
	return reflect.New(scanType).Interface()
}

// setMapValue 创建一个 map 扫描目标并设置到 ret[0]（可能是 slice 追加）
func setMapValue(t reflect.Type, ret []reflect.Value, isSlice bool) (reflect.Value, error) {
	if t.Key().Kind() != reflect.String {
		return reflect.Value{}, fmt.Errorf("map key must be string")
	}
	v := reflect.MakeMap(t)
	if isSlice {
		ret[0] = reflect.Append(ret[0], v)
	} else {
		ret[0] = v
	}
	return v, nil
}

// applyToRet 根据 isSlice 决定把 v 设置到 ret[0] 还是追加到 ret[0]
func applyToRet(v reflect.Value, ret []reflect.Value, isSlice bool) {
	if isSlice {
		ret[0] = reflect.Append(ret[0], v)
	} else {
		ret[0] = v
	}
}

// setValue 创建一个非 map 的扫描目标 + 对应的 deferFn，
// deferFn 会在扫描完成后把值写回 ret[0]（处理 ScanVal / 指针解引用）。
func setValue(t reflect.Type, ret []reflect.Value, isSlice bool) (reflect.Value, []func()) {
	var v reflect.Value
	var deferFn []func()

	if isScanVal(t) {
		// 用户注册了 ScanVal[T] 包装器
		v = reflect.New(getScanValType(t)).Elem()
		deferFn = append(deferFn, func() {
			dst := v
			if t.Kind() == reflect.Pointer {
				dst = getScanValPtr(v)
			} else {
				dst = getScanVal(v)
			}
			applyToRet(dst, ret, isSlice)
		})
		return v, deferFn
	}

	// 普通类型：如果是指针，先解引用得到可扫描的目标
	if t.Kind() == reflect.Pointer {
		v = reflect.New(t.Elem()) // *Elem — sql 会扫进这个
		applyToRet(v, ret, isSlice)
		v = v.Elem() // 把 ret[0] 设好后，继续用 v.Elem() 处理字段
	} else {
		v = reflect.New(t).Elem()
		deferFn = append(deferFn, func() {
			applyToRet(v, ret, isSlice)
		})
	}
	return v, deferFn
}

// GetScanDest 根据返回值 ret 的类型为每一列构造扫描目标切片。
// deferFn 列表会在 sql.Rows.Scan 完成后把临时值写回 ret。
func GetScanDest(columns []*sql.ColumnType, ret []reflect.Value, columnToFieldNameFunc func(column string) string) (destSlice []any, deferFn []func(), err error) {
	if len(ret) == 0 {
		return nil, nil, fmt.Errorf("not scan dest")
	}
	if len(ret) == 1 {
		return handleSingleReturn(columns, ret, columnToFieldNameFunc)
	}
	d, df := handleMultiReturn(columns, ret)
	return d, df, nil
}

// handleSingleReturn 只有一个返回值的场景（struct / map / slice / primitive）
func handleSingleReturn(columns []*sql.ColumnType, ret []reflect.Value, columnToFieldNameFunc func(column string) string) ([]any, []func(), error) {
	t := ret[0].Type()
	var v reflect.Value
	var df []func()

	switch t.Kind() {
	case reflect.Map:
		var err error
		v, err = setMapValue(t, ret, false)
		if err != nil {
			return nil, nil, err
		}
	case reflect.Slice:
		elem := t.Elem()
		if elem.Kind() == reflect.Map {
			var err error
			v, err = setMapValue(elem, ret, true)
			if err != nil {
				return nil, nil, err
			}
		} else {
			v, df = setValue(elem, ret, true)
		}
	default:
		v, df = setValue(t, ret, false)
	}

	var destSlice []any
	var deferFn []func()

	for i, c := range columns {
		switch v.Type().Kind() {
		case reflect.Map:
			valT := v.Type().Elem()
			val := reflect.New(valT).Elem()
			deferFn = append(deferFn, func() {
				v.SetMapIndex(reflect.ValueOf(c.Name()), val)
			})
			destSlice = append(destSlice, val.Addr().Interface())

		case reflect.Struct:
			d, extraDefer := structColumnDest(c, v, i, columnToFieldNameFunc)
			destSlice = append(destSlice, d...)
			deferFn = append(deferFn, extraDefer...)

		default:
			d := scalarColumnDest(c, v, i)
			destSlice = append(destSlice, d)
		}
	}

	deferFn = append(deferFn, df...)
	return destSlice, deferFn, nil
}

// handleMultiReturn 多个返回值的场景（多 return 变量匹配多列）
func handleMultiReturn(columns []*sql.ColumnType, ret []reflect.Value) ([]any, []func()) {
	var destSlice []any
	var deferFn []func()

	for i, c := range columns {
		if i >= len(ret) {
			// 多余列 —— 临时目标
			destSlice = append(destSlice, getTempScanDest(c.ScanType()))
			continue
		}
		t := ret[i].Type()
		switch {
		case isScanVal(t):
			scanV := reflect.New(getScanValType(t)).Elem()
			destSlice = append(destSlice, scanV.Addr().Interface())
			deferFn = append(deferFn, func() {
				if t.Kind() == reflect.Pointer {
					ret[i] = getScanValPtr(scanV)
				} else {
					ret[i] = getScanVal(scanV)
				}
			})
		case isScanValJson(c):
			scanWrap := ShouldScanValJson(c, ret[i])
			destSlice = append(destSlice, scanWrap.Addr().Interface())
			deferFn = append(deferFn, func() {
				ret[i] = getScanValJson(scanWrap)
			})
		default:
			ret[i] = reflect.New(t).Elem()
			destSlice = append(destSlice, ret[i].Addr().Interface())
		}
	}
	return destSlice, deferFn
}

// structColumnDest 为 struct 类型的目标值处理某一列 → 返回 dest + 写回 defer
func structColumnDest(c *sql.ColumnType, v reflect.Value, colIndex int, columnToFieldNameFunc func(string) string) ([]any, []func()) {
	// JSON 列：整列扫进 struct 的 json 字段
	if colIndex == 0 && isScanValJson(c) {
		scanWrap := ShouldScanValJson(c, v)
		return []any{scanWrap.Interface()}, []func(){
			func() { v.Set(getScanValJson(scanWrap)) },
		}
	}

	// 用户注册了 ScanVal 包装器 —— 跳过按字段名映射（这是"struct 本身就是 ScanVal 包装器"的场景）
	if isScanVal(v.Type()) {
		return []any{getTempScanDest(c.ScanType())}, nil
	}

	// 按 column name 找 struct field
	fname := columnToFieldNameFunc(c.Name())
	fv := v.FieldByName(fname)
	if !fv.IsValid() || !fv.CanSet() {
		return []any{getTempScanDest(c.ScanType())}, nil
	}

	// 字段本身是 ScanVal
	if isScanVal(fv.Type()) {
		scanV := reflect.New(getScanValType(fv.Type())).Elem()
		return []any{scanV.Addr().Interface()}, []func(){
			func() {
				if fv.Kind() == reflect.Pointer {
					fv.Set(getScanValPtr(scanV))
				} else {
					fv.Set(getScanVal(scanV))
				}
			},
		}
	}

	// 字段是 JSON 列
	if isScanValJson(c) {
		scanWrap := ShouldScanValJson(c, fv)
		return []any{scanWrap.Interface()}, []func(){
			func() { fv.Set(getScanValJson(scanWrap)) },
		}
	}

	// 普通字段
	return []any{fv.Addr().Interface()}, nil
}

// scalarColumnDest 为非 struct 非 map 的单值目标处理某一列（仅 colIndex==0 时才有效）
func scalarColumnDest(c *sql.ColumnType, v reflect.Value, colIndex int) any {
	if colIndex == 0 && v.CanSet() {
		if isScanValJson(c) {
			scanWrap := ShouldScanValJson(c, v)
			return scanWrap.Interface()
		}
		return v.Addr().Interface()
	}
	return getTempScanDest(c.ScanType())
}
