package tgsql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"github.com/tianxinzizhen/tgsql/util"
)

type sqlFunc struct {
	Args                  []any
	fieldNameToColumnFunc func(string) string // struct 字段名 → 数据库列名（PascalCase → snake_case）
}

// toColumn 将 Go struct 字段名转换为数据库列名
func (sq *sqlFunc) toColumn(fieldName string) string {
	if sq.fieldNameToColumnFunc != nil {
		return sq.fieldNameToColumnFunc(fieldName)
	}
	return fieldName
}

// unwrap 从 map[string]any 传入的 reflect.Value 可能是 interface{} 包装的，
// 需要 .Elem() 解包才能拿到实际类型
func unwrap(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	// 解引用 interface
	if v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	// 解引用指针（支持多级 **T → T）
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return v
		}
		v = v.Elem()
	}
	return v
}

func (sq *sqlFunc) BuildFuncMap() template.FuncMap {
	var sqlFuncMap template.FuncMap = make(template.FuncMap)
	sqlFuncMap["comma"] = sq.comma
	sqlFuncMap["like"] = sq.like
	sqlFuncMap["liker"] = sq.likeRight
	sqlFuncMap["likel"] = sq.likeLeft
	sqlFuncMap["param"] = sq.param
	sqlFuncMap["marshal"] = sq.marshal
	sqlFuncMap["json"] = sq.marshal
	sqlFuncMap["in"] = sq.in
	sqlFuncMap["set"] = sq.set
	sqlFuncMap["where"] = sq.where
	sqlFuncMap["sql"] = sq.sql
	return sqlFuncMap
}

func (*sqlFunc) sql(strs ...string) (string, error) {
	return strings.Join(strs, " "), nil
}

func (sq *sqlFunc) comma(iVal reflect.Value) (string, error) {
	i, isNil := util.Indirect(iVal)
	if isNil {
		return "", fmt.Errorf("comma sql function in paramter is nil")
	}
	sb := &strings.Builder{}
	var commaPrint bool
	switch i.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		commaPrint = i.Int() > 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		commaPrint = i.Uint() > 0
	default:
		return "", nil
	}
	if commaPrint {
		sb.WriteString(",")
	} else {
		sb.WriteString("")
	}
	return sb.String(), nil
}

func (sq *sqlFunc) param(list ...reflect.Value) string {
	sb := &strings.Builder{}
	for i, v := range list {
		if i > 0 {
			sb.WriteString(",")
		}
		v = unwrap(v)
		sq.Args = append(sq.Args, v.Interface())
		sb.WriteString("?")
	}
	return sb.String()
}

func (sq *sqlFunc) like(param reflect.Value) string {
	param = unwrap(param)
	p := fmt.Sprint(param.Interface())
	lb := strings.Builder{}
	if !strings.HasPrefix(p, "%") {
		lb.WriteByte('%')
	}
	lb.WriteString(p)
	if !strings.HasSuffix(p, "%") {
		lb.WriteByte('%')
	}
	sq.Args = append(sq.Args, lb.String())
	return "like ?"
}

func (sq *sqlFunc) likeRight(param reflect.Value) string {
	param = unwrap(param)
	p := fmt.Sprint(param.Interface())
	lb := strings.Builder{}
	lb.WriteString(p)
	if !strings.HasSuffix(p, "%") {
		lb.WriteByte('%')
	}
	sq.Args = append(sq.Args, lb.String())
	return "like ?"
}

func (sq *sqlFunc) likeLeft(param reflect.Value) string {
	param = unwrap(param)
	p := fmt.Sprint(param.Interface())
	lb := strings.Builder{}
	if !strings.HasPrefix(p, "%") {
		lb.WriteByte('%')
	}
	lb.WriteString(p)
	sq.Args = append(sq.Args, lb.String())
	return "like ?"
}

func (sq *sqlFunc) marshal(list ...reflect.Value) (string, error) {
	sb := &strings.Builder{}
	for i, v := range list {
		if i > 0 {
			sb.WriteString(",")
		}
		v = unwrap(v)
		vi := v.Interface()
		mJson, err := json.Marshal(vi)
		if err != nil {
			return "", err
		}
		sb.WriteString("?")
		sq.Args = append(sq.Args, string(mJson))
	}
	return sb.String(), nil
}

func (sq *sqlFunc) in(list ...reflect.Value) string {
	sb := &strings.Builder{}
	sb.WriteString("IN (")
	var num int
	for _, v := range list {
		v = unwrap(v)
		if v.Kind() == reflect.Slice {
			for i := 0; i < v.Len(); i++ {
				if num > 0 {
					sb.WriteString(",")
				}
				num++
				sb.WriteString("?")
				sq.Args = append(sq.Args, v.Index(i).Interface())
			}
		} else {
			if num > 0 {
				sb.WriteString(",")
			}
			num++
			sb.WriteString("?")
			sq.Args = append(sq.Args, v.Interface())
		}
	}
	sb.WriteString(")")
	return sb.String()
}

// buildConditionList 公共实现：把 map/struct/alias-list 渲染为 col = ? 列表
// separator: set=","  where=" and "
// 跳过 template.IsTrue 为零值的字段
func (sq *sqlFunc) buildConditionList(list []reflect.Value, separator string) (string, error) {
	sb := &strings.Builder{}
	preAlias := ""
	var num int
	for _, param := range list {
		param = unwrap(param)
		switch param.Kind() {
		case reflect.String:
			if preAlias == "" {
				preAlias = param.String() + "."
			} else {
				sb.WriteString(param.String())
			}
		case reflect.Map:
			if param.Type().Key().Kind() != reflect.String {
				preAlias = ""
				continue
			}
			iter := param.MapRange()
			for iter.Next() {
				name := sq.toColumn(iter.Key().String())
				if num > 0 {
					sb.WriteString(separator)
				}
				num++
				sb.WriteString(preAlias)
				sb.WriteString(name)
				sb.WriteString(" = ?")
				sq.Args = append(sq.Args, iter.Value().Interface())
			}
			preAlias = ""
		case reflect.Struct:
			for i := 0; i < param.NumField(); i++ {
				val := param.Field(i).Interface()
				if truth, ok := template.IsTrue(val); ok && truth {
					name := sq.toColumn(param.Type().Field(i).Name)
					if num > 0 {
						sb.WriteString(separator)
					}
					num++
					sb.WriteString(preAlias)
					sb.WriteString(name)
					sb.WriteString(" = ?")
					sq.Args = append(sq.Args, val)
				}
			}
			preAlias = ""
		default:
			return "", fmt.Errorf("sql function parameter is not string, map or struct")
		}
	}
	return sb.String(), nil
}

func (sq *sqlFunc) set(list ...reflect.Value) (string, error) {
	return sq.buildConditionList(list, ",")
}

func (sq *sqlFunc) where(list ...reflect.Value) (string, error) {
	return sq.buildConditionList(list, " and ")
}
