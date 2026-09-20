package sqlval

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
)

type Convert[T any] interface {
	ConvertValue(v T) (any, error)
	ConvertValuePtr(v *T) (any, error)
}

var localConvertValMap = make(map[reflect.Type]reflect.Value)

// typedNilError 是 (interface{})(nil)，用于构造 reflect.ValueOf(typedNilError)，
// 得到的是 "typed nil" Value（IsValid=true, IsNil=true），而不是零值 Value。
// 这样调用方可以安全检查 IsNil() 后再做 .(error) 断言。
var typedNilError error = nil

func RegisterConvert[T any](gv Convert[T]) error {
	if reflect.TypeFor[T]().Kind() == reflect.Pointer {
		return fmt.Errorf("gv.ConvertValuePtr() must be not pointer")
	}
	localConvertValMap[reflect.TypeFor[T]()] = reflect.ValueOf(gv.ConvertValue)
	localConvertValMap[reflect.TypeFor[*T]()] = reflect.ValueOf(gv.ConvertValuePtr)
	return nil
}

// nilErrVal 返回一个 typed nil error 的 reflect.Value（IsValid=true, IsNil=true）
func nilErrVal() reflect.Value {
	return reflect.ValueOf(typedNilError)
}

func localConvertVal(v reflect.Value) []reflect.Value {
	if method, ok := localConvertValMap[v.Type()]; ok {
		if method.IsValid() {
			return method.Call([]reflect.Value{v})
		}
	} else {
		jv := v
		for jv.Kind() == reflect.Pointer {
			jv = jv.Elem()
		}
		switch jv.Kind() {
		case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
			if !jv.IsValid() {
				return []reflect.Value{v, nilErrVal()}
			}
			mJson, err := json.Marshal(jv.Interface())
			if err != nil {
				return []reflect.Value{v, reflect.ValueOf(err)}
			}
			return []reflect.Value{reflect.ValueOf(string(mJson)), nilErrVal()}
		}
	}
	return []reflect.Value{v, nilErrVal()}
}

func ConvertValue(ci any, v any) (any, error) {
	var err error
	nvc, _ := ci.(driver.NamedValueChecker)
	switch {
	case nvc != nil:
		nv := &driver.NamedValue{
			Value: v,
		}
		err = nvc.CheckNamedValue(nv)
		if err == nil {
			return nv.Value, nil
		}
		// fallthrough 到默认参数转换器
		fallthrough
	default:
		cv, err2 := driver.DefaultParameterConverter.ConvertValue(v)
		if err2 == nil {
			return cv, nil
		}
		ret := localConvertVal(reflect.ValueOf(v))
		if ret[1].IsValid() && !ret[1].IsNil() {
			return ret[0].Interface(), ret[1].Interface().(error)
		}
		return ret[0].Interface(), nil
	}
}

func ConvertValues(ci any, args []any) ([]any, error) {
	ret := make([]any, 0, len(args))
	for _, arg := range args {
		v, err := ConvertValue(ci, arg)
		if err != nil {
			return nil, err
		}
		ret = append(ret, v)
	}
	return ret, nil
}
