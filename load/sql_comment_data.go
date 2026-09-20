package load

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// SqlDataInfo 描述一个 //sql / /*sql 注解对应的 SQL 语句
type SqlDataInfo struct {
	TypeName    string
	FuncName    string
	Name        string
	Sql         string
	NotPrepare  bool
	BatchInsert bool
	Param       []string
}

// loadCommentBytes 解析 bytes 中的 Go 源码，提取所有嵌入在 struct 字段注释里的 SQL 模板
func loadCommentBytes(pkg string, bytes []byte) ([]*SqlDataInfo, error) {
	if bytes == nil {
		return nil, fmt.Errorf("sql go bytes is nil")
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", bytes, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var result []*SqlDataInfo
	seen := map[string]struct{}{}

	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			sInfos, err := parseStructFields(pkg, typeSpec.Name.String(), structType)
			if err != nil {
				return nil, err
			}
			for _, si := range sInfos {
				if _, dup := seen[si.Name]; dup {
					return nil, fmt.Errorf("%s.%s duplicate sql field name[%s]", pkg, typeSpec.Name.String(), si.Name)
				}
				seen[si.Name] = struct{}{}
				result = append(result, si)
			}
		}
	}
	return result, nil
}

// parseStructFields 扫描一个 struct 的所有字段，收集带 SQL 注解的 func 类型字段
func parseStructFields(pkg, typeName string, st *ast.StructType) ([]*SqlDataInfo, error) {
	var result []*SqlDataInfo
	for _, field := range st.Fields.List {
		fc, ok := field.Type.(*ast.FuncType)
		if !ok || field.Doc == nil {
			continue
		}
		// 每个字段的 doc comment 里可能有多行注释，但最多会有一个 sql 注解
		for _, ci := range field.Doc.List {
			sqlBody, ok := extractSQLBody(ci.Text)
			if !ok {
				continue
			}
			if len(field.Names) == 0 {
				continue
			}
			si := &SqlDataInfo{
				TypeName: fmt.Sprintf("%s.%s", pkg, typeName),
				Name:     field.Names[0].String(),
				FuncName: fmt.Sprintf("%s.%s.%s:", pkg, typeName, field.Names[0].String()),
				Sql:      sqlBody,
				Param:    collectParamNames(fc),
			}
			if err := applyOption(si); err != nil {
				return nil, err
			}
			result = append(result, si)
		}
	}
	return result, nil
}

// extractSQLBody 从 //sql 或 /*sql 注释文本里抽出纯 SQL 内容
// ok=false 表示这不是 SQL 注解（应跳过）
func extractSQLBody(comment string) (string, bool) {
	switch {
	case strings.HasPrefix(comment, "//sql"):
		return comment[5:], true
	case strings.HasPrefix(comment, "/*sql"):
		return comment[5 : len(comment)-2], true
	}
	return "", false
}

// applyOption 解析 SQL 开头的 ?option{key:value,...} 指令，就地填充 SqlDataInfo
func applyOption(si *SqlDataInfo) error {
	if !strings.HasPrefix(si.Sql, "?option{") {
		return nil
	}
	optionStr, rest, ok := strings.Cut(si.Sql, "}")
	if !ok {
		return nil // 没闭合，当成普通 SQL 处理
	}
	si.Sql = strings.TrimSpace(rest)
	optionStr = strings.TrimPrefix(optionStr, "?option{")

	for _, pair := range strings.Split(optionStr, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "not_prepare":
			si.NotPrepare = strings.TrimSpace(v) == "true"
		case "batch_insert":
			si.BatchInsert = strings.TrimSpace(v) == "true"
		case "name":
			si.Name = strings.TrimSpace(v)
		}
	}
	return nil
}

// collectParamNames 从函数签名里收集所有参数名（跳过类型）
func collectParamNames(fc *ast.FuncType) []string {
	var names []string
	for _, p := range fc.Params.List {
		for _, n := range p.Names {
			if n.Name != "" {
				names = append(names, n.Name)
			}
		}
	}
	return names
}
