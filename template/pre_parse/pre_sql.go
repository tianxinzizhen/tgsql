package preparse

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// 已知的Go模板关键字
var templateKeywords = map[string]bool{
	"if":       true,
	"end":      true,
	"range":    true,
	"with":     true,
	"else":     true,
	"define":   true,
	"block":    true,
	"template": true,
	"nil":      true,
}

// 已知的tgsql模板函数
var templateFunctions = map[string]bool{
	"like":    true,
	"liker":   true,
	"likel":   true,
	"param":   true,
	"in":      true,
	"set":     true,
	"where":   true,
	"json":    true,
	"marshal": true,
	"comma":   true,
	"sql":     true,
}

// Config 预处理配置
type Config struct {
	LeftDelim         string              // 左分隔符，默认 "{"
	RightDelim        string              // 右分隔符，默认 "}"
	ColumnToFieldName func(string) string // 列名到结构体字段名的转换函数
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		LeftDelim:         "{",
		RightDelim:        "}",
		ColumnToFieldName: IdentityColumnToFieldName,
	}
}

// IdentityColumnToFieldName 恒等转换：保持列名原样
// 这是默认行为，用户可以通过 Config.ColumnToFieldName 自定义转换规则
func IdentityColumnToFieldName(column string) string {
	return column
}

// SnakeToPascalCase 将 snake_case 转换为 PascalCase
// "user_name" -> "UserName", "create_time" -> "CreateTime", "id" -> "Id"
func SnakeToPascalCase(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		r, w := utf8.DecodeRuneInString(part)
		result.WriteRune(unicode.ToUpper(r))
		result.WriteString(part[w:])
	}
	return result.String()
}

// Preprocess 预处理SQL模板，将简化语法转换为标准Go模板语法
func Preprocess(sql string, cfg Config) string {
	if cfg.LeftDelim == "" {
		cfg.LeftDelim = "{"
	}
	if cfg.RightDelim == "" {
		cfg.RightDelim = "}"
	}
	if cfg.ColumnToFieldName == nil {
		cfg.ColumnToFieldName = DefaultConfig().ColumnToFieldName
	}

	var sb strings.Builder
	i := 0
	n := len(sql)

	for i < n {
		// 跳过SQL字符串（单引号）
		if sql[i] == '\'' {
			end := skipSQLString(sql, i, '\'')
			sb.WriteString(sql[i:end])
			i = end
			continue
		}
		// 跳过双引号字符串
		if sql[i] == '"' {
			end := skipSQLString(sql, i, '"')
			sb.WriteString(sql[i:end])
			i = end
			continue
		}
		// 跳过反引号字符串
		if sql[i] == '`' {
			end := skipRawString(sql, i)
			sb.WriteString(sql[i:end])
			i = end
			continue
		}

		// 检测左分隔符 {
		if strings.HasPrefix(sql[i:], cfg.LeftDelim) {
			// 先 peek 看里面是否是 set / where 函数调用
			funcName := peekTemplateFuncName(sql, i, cfg)
			switch funcName {
			case "set":
				// 检查前面有没有 SET 关键字
				if !scanBackwardKeyword(sql, i, "SET") {
					sb.WriteString("SET ")
				}
			case "where":
				if !scanBackwardKeyword(sql, i, "WHERE") {
					// 前面没有 WHERE 关键字 → 补 WHERE
					sb.WriteString("WHERE ")
				} else {
					// 前面有 WHERE 子句 → 看紧挨着的 identifier
					// 是 AND/OR → 什么都不补
					// 是 WHERE 本身（中间没别的）→ 什么都不补（WHERE 后面的第一个条件）
					// 是别的（如 1, user_name, id）→ 补 AND 连接
					if !precededByKeyword(sql, i, "AND", "OR", "WHERE") {
						sb.WriteString("AND ")
					}
				}
			}

			result, consumed := processTemplateAction(sql, i, cfg)
			sb.WriteString(result)
			i += consumed
			continue
		}

		// 检测方括号 [
		if sql[i] == '[' {
			result, consumed := processSquareBracket(sql, i, cfg)
			sb.WriteString(result)
			i += consumed
			continue
		}

		// 检测 @field (不在大括号内的@引用)
		if sql[i] == '@' {
			result, consumed := processAtSign(sql, i, cfg)
			sb.WriteString(result)
			i += consumed
			continue
		}

		// 检测 like ? 和 in ? 隐式转换
		if processed, consumed := processLikeInQuestion(sql, i, cfg); consumed > 0 {
			sb.WriteString(processed)
			i += consumed
			continue
		}

		// 检测 column_name = ? 隐式转换
		if processed, consumed := processColumnEqualsQuestion(sql, i, cfg); consumed > 0 {
			sb.WriteString(processed)
			i += consumed
			continue
		}

		// 检测 INSERT ... VALUES(?) 隐式展开
		if processed, consumed := processInsertValuesExpand(sql, i, cfg); consumed > 0 {
			sb.WriteString(processed)
			i += consumed
			continue
		}

		// 普通字符
		sb.WriteByte(sql[i])
		i++
	}

	return sb.String()
}

// skipSQLString 跳过SQL字符串（单引号或双引号），处理转义
func skipSQLString(s string, start int, quote byte) int {
	i := start + 1
	n := len(s)
	for i < n {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == quote {
			// SQL中单引号转义 ''
			if quote == '\'' && i+1 < n && s[i+1] == '\'' {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

// skipRawString 跳过反引号字符串
func skipRawString(s string, start int) int {
	i := start + 1
	n := len(s)
	for i < n {
		if s[i] == '`' {
			return i + 1
		}
		i++
	}
	return i
}

// processTemplateAction 处理大括号内的模板动作
// 返回转换后的字符串和消费的字节数
func processTemplateAction(sql string, start int, cfg Config) (string, int) {
	leftLen := len(cfg.LeftDelim)
	rightLen := len(cfg.RightDelim)

	// 找到匹配的右分隔符
	contentStart := start + leftLen
	end := findMatchingDelim(sql, contentStart, cfg)
	if end == -1 {
		// 没有匹配的右分隔符，原样返回
		return sql[start:], len(sql) - start
	}

	rightEnd := end + rightLen

	content := sql[contentStart:end]
	trimmedContent := strings.TrimSpace(content)

	// 检测 whitespace trimming 标记
	// Go template 的 whitespace trimming:
	//   {- ... 左 delim 后加 "- " 表示修剪前面的空白
	//   ... -} 右 delim 前加 " -" 表示修剪后面的空白
	leftTrimPrefix := false
	rightTrimSuffix := false

	if strings.HasPrefix(trimmedContent, "- ") || strings.HasPrefix(trimmedContent, "-") {
		// 检查是不是 whitespace trim 标记（不是负数或减号）
		if len(trimmedContent) > 1 && (trimmedContent[1] == ' ' || trimmedContent[1] == '\t') {
			leftTrimPrefix = true
			trimmedContent = strings.TrimPrefix(trimmedContent, "- ")
		}
	}

	if strings.HasSuffix(trimmedContent, " -") {
		rightTrimSuffix = true
		trimmedContent = strings.TrimSuffix(trimmedContent, " -")
	}

	// 检查 trim 后的内容是否为空
	if trimmedContent == "" {
		// 只有 trim 标记，保持原样
		return sql[start:rightEnd], rightEnd - start
	}

	// 转换内部内容
	newContent := transformTemplateContent(trimmedContent, cfg)

	// 重新组装，保留 trim 标记
	var result strings.Builder
	result.WriteString(cfg.LeftDelim)
	if leftTrimPrefix {
		result.WriteString("- ")
	}
	result.WriteString(newContent)
	if rightTrimSuffix {
		result.WriteString(" -")
	}
	result.WriteString(cfg.RightDelim)

	return result.String(), rightEnd - start
}

// findMatchingDelim 找到匹配的右分隔符位置，支持嵌套
func findMatchingDelim(sql string, from int, cfg Config) int {
	depth := 1
	i := from
	n := len(sql)

	for i < n {
		// 跳过字符串
		if sql[i] == '\'' {
			i = skipSQLString(sql, i, '\'')
			continue
		}
		if sql[i] == '"' {
			i = skipSQLString(sql, i, '"')
			continue
		}
		if sql[i] == '`' {
			i = skipRawString(sql, i)
			continue
		}

		if strings.HasPrefix(sql[i:], cfg.LeftDelim) {
			depth++
			i += len(cfg.LeftDelim)
			continue
		}
		if strings.HasPrefix(sql[i:], cfg.RightDelim) {
			depth--
			if depth == 0 {
				return i
			}
			i += len(cfg.RightDelim)
			continue
		}
		i++
	}
	return -1
}

// transformTemplateContent 转换模板内容
func transformTemplateContent(content string, cfg Config) string {
	// 拆分token（按空格和逗号分隔，但要考虑函数参数）
	tokens := splitTemplateTokens(content)
	if len(tokens) == 0 {
		return content
	}

	firstToken := strings.TrimSpace(tokens[0])

	// 检查是否是关键字或函数调用
	if templateKeywords[firstToken] || templateFunctions[firstToken] {
		// 是关键字或函数调用，保持原样但处理内部的@符号和多字段
		return transformFunctionArgs(content, cfg)
	}

	// 检查第一个token是否以.开头（已经是Go模板路径）
	if strings.HasPrefix(firstToken, ".") {
		// 可能是多字段展开：.id, .name -> .id, .name （保持但规范化）
		return expandMultiField(content, cfg)
	}

	// 检查第一个token是否以@开头
	if strings.HasPrefix(firstToken, "@") {
		// @id -> .id
		return expandAtFields(content, cfg)
	}

	// 纯标识符：id -> .id
	return expandPlainFields(content, cfg)
}

// splitTemplateTokens 按空格拆分模板内容，保留引号内的内容
func splitTemplateTokens(content string) []string {
	var tokens []string
	var current strings.Builder
	i := 0
	n := len(content)

	for i < n {
		c := content[i]

		// 处理引号
		if c == '\'' || c == '"' || c == '`' {
			quote := c
			current.WriteByte(c)
			i++
			for i < n && content[i] != quote {
				if content[i] == '\\' && i+1 < n {
					current.WriteByte(content[i])
					current.WriteByte(content[i+1])
					i += 2
					continue
				}
				current.WriteByte(content[i])
				i++
			}
			if i < n {
				current.WriteByte(content[i])
				i++
			}
			continue
		}

		if c == ' ' || c == '\t' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			i++
			continue
		}

		current.WriteByte(c)
		i++
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// fieldJoinStr 生成多字段之间的连接字符串
// 逗号分隔: "}, {.name" 或 "}}, {{.Name"
func fieldJoinStr(cfg Config) string {
	return cfg.RightDelim + ", " + cfg.LeftDelim
}

// expandMultiField 处理已经有点号的多字段展开
// ".id, .name" -> "param .id}, {param .name"
// ".Id" -> "param .Id"
// 每个字段都用 param 包裹以保证参数化安全
func expandMultiField(content string, cfg Config) string {
	// 清除 . 前缀，统一走 param 包装
	wrapOne := func(field string) string {
		field = strings.TrimSpace(field)
		field = strings.TrimPrefix(field, ".")
		// 如果已经是 "param .xxx" 形式（极少见但保险），不重复包装
		if strings.HasPrefix(field, "param ") {
			return field
		}
		// 同样做 ColumnToFieldName 转换，和 expandPlainFields 保持一致
		if cfg.ColumnToFieldName != nil {
			field = cfg.ColumnToFieldName(field)
		}
		return "param ." + field
	}

	// 检查是否有逗号分隔的多字段
	if strings.Contains(content, ",") {
		parts := strings.Split(content, ",")
		var results []string
		for _, part := range parts {
			results = append(results, wrapOne(part))
		}
		return strings.Join(results, fieldJoinStr(cfg))
	}

	// 空格分隔的多字段
	tokens := splitTemplateTokens(content)
	if len(tokens) > 1 {
		var results []string
		for _, tok := range tokens {
			results = append(results, wrapOne(tok))
		}
		return strings.Join(results, fieldJoinStr(cfg))
	}

	// 单字段
	return wrapOne(content)
}

// expandAtFields 处理@符号开头的字段
// "@id" -> "param .Id"
// "@id, @name" -> "param .Id}, {param .Name"
// "@id @name" -> "param .Id}, {param .Name"
func expandAtFields(content string, cfg Config) string {
	var fields []string

	// 按逗号拆分
	if strings.Contains(content, ",") {
		parts := strings.Split(content, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			fields = append(fields, part)
		}
	} else {
		// 按空格拆分
		tokens := splitTemplateTokens(content)
		for _, tok := range tokens {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			fields = append(fields, tok)
		}
	}

	var results []string
	for _, field := range fields {
		// 去掉@前缀
		field = strings.TrimPrefix(field, "@")
		// 转换蛇形到帕斯卡
		if cfg.ColumnToFieldName != nil {
			field = cfg.ColumnToFieldName(field)
		}
		results = append(results, "param ."+field)
	}

	if len(results) == 1 {
		return results[0]
	}
	return strings.Join(results, fieldJoinStr(cfg))
}

// expandPlainFields 处理纯标识符字段
// "id" -> "param .Id"
// "id, name" -> "param .Id}, {param .Name"
// "id name" -> "param .Id}, {param .Name"
// 注意：返回值会被 processTemplateAction 包进外层 {}，所以多字段时中间的 } 和 { 由 fieldJoinStr 提供
func expandPlainFields(content string, cfg Config) string {
	var fields []string

	// 按逗号拆分
	if strings.Contains(content, ",") {
		parts := strings.Split(content, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			fields = append(fields, part)
		}
	} else {
		// 按空格拆分
		tokens := splitTemplateTokens(content)
		for _, tok := range tokens {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			fields = append(fields, tok)
		}
	}

	var results []string
	for _, field := range fields {
		// 去掉可能的@前缀
		field = strings.TrimPrefix(field, "@")
		// 去掉可能的.前缀
		field = strings.TrimPrefix(field, ".")
		// 转换蛇形到帕斯卡
		if cfg.ColumnToFieldName != nil {
			field = cfg.ColumnToFieldName(field)
		}
		// 参数化包装: param .FieldName
		results = append(results, "param ."+field)
	}

	if len(results) == 1 {
		return results[0]
	}
	return strings.Join(results, fieldJoinStr(cfg))
}

// transformFunctionArgs 处理函数内部的参数
// 如 {set .user} 保持不变，{param @id @name} -> {param .id .name}
func transformFunctionArgs(content string, cfg Config) string {
	tokens := splitTemplateTokens(content)
	if len(tokens) <= 1 {
		return content
	}

	firstToken := tokens[0]
	if !templateFunctions[firstToken] {
		return content
	}

	// 转换参数中的 @field
	var newTokens []string
	newTokens = append(newTokens, firstToken)
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		tok = transformSingleField(tok, cfg)
		newTokens = append(newTokens, tok)
	}

	return strings.Join(newTokens, " ")
}

// transformSingleField 转换单个字段引用
func transformSingleField(field string, cfg Config) string {
	field = strings.TrimSpace(field)

	if field == "" {
		return field
	}

	// 已经是点号开头或字符串字面量，保持原样
	if strings.HasPrefix(field, ".") || strings.HasPrefix(field, `"`) || strings.HasPrefix(field, "'") {
		return field
	}

	// @符号开头
	if strings.HasPrefix(field, "@") {
		field = strings.TrimPrefix(field, "@")
		if cfg.ColumnToFieldName != nil {
			field = cfg.ColumnToFieldName(field)
		}
		return "." + field
	}

	// 纯标识符
	if isIdentifier(field) {
		if cfg.ColumnToFieldName != nil {
			field = cfg.ColumnToFieldName(field)
		}
		return "." + field
	}

	return field
}

// isIdentifier 检查是否是标识符（字母数字下划线）
func isIdentifier(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
		} else {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

// processAtSign 处理SQL体中的 @field 引用（不在大括号内）
func processAtSign(sql string, start int, cfg Config) (string, int) {
	// @后面必须跟标识符
	end := start + 1
	n := len(sql)
	for end < n {
		r := rune(sql[end])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		end++
	}

	if end == start+1 {
		// @后面没有标识符，原样返回
		return "@", 1
	}

	fieldName := sql[start+1 : end]
	var transformedName string
	if cfg.ColumnToFieldName != nil {
		transformedName = cfg.ColumnToFieldName(fieldName)
	} else {
		transformedName = fieldName
	}

	result := cfg.LeftDelim + "param ." + transformedName + cfg.RightDelim
	return result, end - start
}

// processLikeInQuestion 处理 SQL 中的 `like ?` 和 `in ?` 隐式转换
// user_name like ?  ->  user_name {like .UserName}
// id in ?           ->  id {in .Id}
// start 参数是 identifier 的起始位置
func processLikeInQuestion(sql string, start int, cfg Config) (string, int) {
	n := len(sql)
	i := start

	// 检查当前位置是否是标识符起始（必须是字母或下划线）
	if i >= n {
		return "", 0
	}
	r := rune(sql[i])
	if !unicode.IsLetter(r) && r != '_' {
		return "", 0
	}

	// 提取标识符
	identStart := i
	for i < n {
		r := rune(sql[i])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		i++
	}
	identName := sql[identStart:i]

	// 跳过空白
	for i < n && isSpace(rune(sql[i])) {
		i++
	}

	if i >= n {
		return "", 0
	}

	// 检查是否是 like 或 in 关键字
	keywordStart := i
	for i < n && (unicode.IsLetter(rune(sql[i])) || unicode.IsDigit(rune(sql[i]))) {
		i++
	}
	if i == keywordStart {
		return "", 0
	}
	keyword := strings.ToLower(sql[keywordStart:i])

	var funcName string
	switch keyword {
	case "like":
		funcName = "like"
	case "in":
		funcName = "in"
	default:
		return "", 0
	}

	if i >= n {
		return "", 0
	}

	// 跳过关键字和 ? 之间的空白
	questionStart := i
	for questionStart < n && isSpace(rune(sql[questionStart])) {
		questionStart++
	}

	if questionStart >= n || sql[questionStart] != '?' {
		return "", 0
	}

	// 转换字段名
	var fieldRef string
	if cfg.ColumnToFieldName != nil {
		fieldRef = cfg.ColumnToFieldName(identName)
	} else {
		fieldRef = identName
	}

	// 替换整个 "identifier like ?" 或 "identifier in ?"
	// result 包含原 identifier 和新的模板调用，保持语法正确
	consumed := questionStart + 1 - identStart
	// like 函数返回 "like ?" —— 不需要额外关键字
	// in 函数返回 "(?,...)" —— 需要保留 IN 关键字
	var result string
	switch funcName {
	case "in":
		result = identName + " IN " + cfg.LeftDelim + funcName + " ." + fieldRef + cfg.RightDelim
	default:
		result = identName + " " + cfg.LeftDelim + funcName + " ." + fieldRef + cfg.RightDelim
	}

	return result, consumed
}

// processColumnEqualsQuestion 处理 SQL 中的 `column = ?` / `column != ?` / `column > ?` 等隐式转换
// id = ?     ->  id = {param .Id}
// age > ?    ->  age > {param .Age}
// name != ?  ->  name != {param .Name}
// 所有值都走 param 函数参数化，避免 SQL 注入
func processColumnEqualsQuestion(sql string, start int, cfg Config) (string, int) {
	n := len(sql)
	i := start

	// 当前位置必须是标识符起始
	if i >= n {
		return "", 0
	}
	r := rune(sql[i])
	if !unicode.IsLetter(r) && r != '_' {
		return "", 0
	}

	// 提取标识符
	identStart := i
	for i < n {
		r := rune(sql[i])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		i++
	}
	identName := sql[identStart:i]

	// 跳过空白
	for i < n && isSpace(rune(sql[i])) {
		i++
	}

	if i >= n {
		return "", 0
	}

	// 检测比较运算符 = != <> > >= < <=
	opStart := i
	opLen := 0
	switch {
	case i+1 < n && sql[i] == '!' && sql[i+1] == '=':
		opLen = 2
	case i+1 < n && sql[i] == '<' && sql[i+1] == '>':
		opLen = 2
	case i+1 < n && sql[i] == '<' && sql[i+1] == '=':
		opLen = 2
	case i+1 < n && sql[i] == '>' && sql[i+1] == '=':
		opLen = 2
	case sql[i] == '=' || sql[i] == '>' || sql[i] == '<':
		opLen = 1
	default:
		return "", 0
	}
	op := sql[opStart : opStart+opLen]
	i = opStart + opLen

	// 跳过运算符后的空白
	for i < n && isSpace(rune(sql[i])) {
		i++
	}

	if i >= n || sql[i] != '?' {
		return "", 0
	}

	// 转换字段名
	var fieldRef string
	if cfg.ColumnToFieldName != nil {
		fieldRef = cfg.ColumnToFieldName(identName)
	} else {
		fieldRef = identName
	}

	// 替换整个 "identifier OP ?"
	consumed := i + 1 - identStart
	result := identName + " " + op + " " + cfg.LeftDelim + "param ." + fieldRef + cfg.RightDelim

	return result, consumed
}

// processInsertValuesExpand 处理 INSERT 语句中 VALUES(?) 隐式展开
// INSERT INTO t (id, name, age) VALUES(?)  →  VALUES({param .Id .Name .Age})
// INSERT INTO t (id, name, age) VALUES(?, ?) → 不处理（用户显式写了多个 ?）
// INSERT INTO t (id, name, age) VALUES(?), (?) → 多行 VALUES 都展开
// INSERT INTO t VALUES(?) → 无列名列表，跳过
//
// start 是 VALUES 关键字的起始位置（i 处）
func processInsertValuesExpand(sql string, start int, cfg Config) (string, int) {
	n := len(sql)
	i := start

	// 1. 提取当前标识符，必须是 VALUES
	if i >= n {
		return "", 0
	}
	r := rune(sql[i])
	if !unicode.IsLetter(r) && r != '_' {
		return "", 0
	}
	identStart := i
	for i < n {
		r := rune(sql[i])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		i++
	}
	if strings.ToLower(sql[identStart:i]) != "values" {
		return "", 0
	}
	// valuesEnd 未使用 — 保留以对齐逻辑结构
	_ = i

	// 2. 跳过 VALUES 后的空白
	for i < n && isSpace(rune(sql[i])) {
		i++
	}
	if i >= n || sql[i] != '(' {
		return "", 0
	}

	// 3. 只解析第一组 VALUES 括号
	// 多行批量 VALUES(?), (?) 不可控 —— 只有单行 VALUES(?) 才展开
	firstOpen := i
	depth := 1
	i++
	for i < n && depth > 0 {
		if sql[i] == '\'' {
			i = skipSQLString(sql, i, '\'')
			continue
		}
		if sql[i] == '"' {
			i = skipSQLString(sql, i, '"')
			continue
		}
		if sql[i] == '`' {
			i = skipRawString(sql, i)
			continue
		}
		if sql[i] == '(' {
			depth++
		} else if sql[i] == ')' {
			depth--
			if depth == 0 {
				i++
				break
			}
		}
		i++
	}
	firstClose := i - 1
	content := sql[firstOpen+1 : firstClose]
	if !isOnlyQuestionMarks(content) {
		return "", 0
	}

	// 检查后面是否还有更多 VALUES 组 —— 多行则放弃展开
	j := i
	for j < n && isSpace(rune(sql[j])) {
		j++
	}
	if j < n && sql[j] == ',' {
		return "", 0 // 多行 VALUES(?), (?) — 不展开
	}

	// 4. 往回找 INSERT 语句的列名列表 (...)
	colNames := findInsertColumnList(sql, start)
	if len(colNames) == 0 {
		// 没有显式列名列表 → 无法展开
		return "", 0
	}

	// 5. 转换列名 → 字段引用
	var fieldRefs []string
	for _, col := range colNames {
		var fieldName string
		if cfg.ColumnToFieldName != nil {
			fieldName = cfg.ColumnToFieldName(col)
		} else {
			fieldName = col
		}
		fieldRefs = append(fieldRefs, "."+fieldName)
	}
	paramArgs := strings.Join(fieldRefs, " ")

	// 6. 构造替换后的 VALUES 部分
	var result strings.Builder
	result.WriteString(sql[identStart:firstOpen]) // VALUES 关键字 + 前导空白
	result.WriteString("(")
	result.WriteString(cfg.LeftDelim)
	result.WriteString("param ")
	result.WriteString(paramArgs)
	result.WriteString(cfg.RightDelim)
	result.WriteString(")")

	consumed := firstClose + 1 - identStart
	return result.String(), consumed
}

// isOnlyQuestionMarks 检查 VALUES 括号内容是否是**单个** ?（带可选空白）
// 用于判断是否可以从 INSERT 列名列表展开多列参数
func isOnlyQuestionMarks(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "?" {
		return true
	}
	return false
}

// findInsertColumnList 从 INSERT 语句中提取列名列表
// 例如 "INSERT INTO t (id, name, age) VALUES(?)" 中找到 (id, name, age)
// 返回列名数组（已去掉反引号和空格）
func findInsertColumnList(sql string, valuesPos int) []string {
	// 往回扫描找 INSERT 关键字
	insertPos := -1
	for i := valuesPos - 1; i >= 0; i-- {
		r := rune(sql[i])
		if unicode.IsLetter(r) || r == '_' {
			// 提取整个 identifier
			end := i + 1
			for i >= 0 {
				r := rune(sql[i])
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
					break
				}
				i--
			}
			start := i + 1
			ident := strings.ToUpper(sql[start:end])
			if ident == "INSERT" {
				insertPos = start
				break
			}
		}
	}
	if insertPos == -1 {
		return nil
	}

	// 从 INSERT 之后找第一个 (...)
	// 跳过 INTO, table name, 空白
	i := insertPos + 6 // "INSERT" 长度
	n := len(sql)

	// 往下找第一个 ( 在 VALUES 之前
	parenOpen := -1
	for i < n && i < valuesPos {
		if sql[i] == '(' {
			// 检查这个 ( 是否是 VALUES 前面的列名列表
			// 方式：解析括号内所有 identifier，确认都是列名（没有 ? 模板字符）
			depth := 1
			j := i + 1
			for j < n && depth > 0 {
				if sql[j] == '\'' {
					j = skipSQLString(sql, j, '\'')
					continue
				}
				if sql[j] == '"' {
					j = skipSQLString(sql, j, '"')
					continue
				}
				if sql[j] == '`' {
					j = skipRawString(sql, j)
					continue
				}
				if sql[j] == '(' {
					depth++
				} else if sql[j] == ')' {
					depth--
					if depth == 0 {
						parenOpen = i
						break
					}
				}
				j++
			}
			if parenOpen != -1 {
				break
			}
		}
		i++
	}
	if parenOpen == -1 {
		return nil
	}

	// 解析列名列表
	depth := 1
	j := parenOpen + 1
	var colParts []string
	var current strings.Builder
	for j < n && depth > 0 {
		c := sql[j]
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				break
			}
		} else if c == ',' {
			colParts = append(colParts, current.String())
			current.Reset()
		} else if c == '`' {
			// 反引号内的列名 — 去掉反引号
			j++
			for j < n && sql[j] != '`' {
				current.WriteByte(sql[j])
				j++
			}
		} else if !isSpace(rune(c)) {
			current.WriteByte(c)
		}
		j++
	}
	if current.Len() > 0 {
		colParts = append(colParts, current.String())
	}

	// 过滤掉可能的 schema 前缀 dbo.t.col
	var colNames []string
	for _, part := range colParts {
		// 如果含 . 取最后一段
		if idx := strings.LastIndex(part, "."); idx >= 0 {
			part = part[idx+1:]
		}
		part = strings.TrimSpace(part)
		if part != "" {
			colNames = append(colNames, part)
		}
	}

	return colNames
}

// processSquareBracket 处理方括号可选条件 [condition]
func processSquareBracket(sql string, start int, cfg Config) (string, int) {
	// 找到匹配的 ]
	end := findMatchingBracket(sql, start)
	if end == -1 {
		// 没有匹配的 ]，原样返回
		return sql[start:], len(sql) - start
	}

	content := sql[start+1 : end]
	bracketEnd := end + 1

	// 处理方括号内的内容（可能包含模板动作和@引用）
	innerProcessed := Preprocess(content, cfg)

	// 从条件中提取字段名用于 if 判断
	fieldName := extractFieldFromCondition(content, cfg)
	if fieldName == "" {
		// 如果无法提取字段名，使用整个条件作为判断
		fieldName = "." + extractFirstWord(strings.TrimSpace(content))
	}

	if cfg.ColumnToFieldName != nil {
		fieldName = "." + cfg.ColumnToFieldName(strings.TrimPrefix(fieldName, "."))
	}

	// 生成 {- if .field} inner {end -}
	// 使用 Go template 的 whitespace trimming:
	//   {-  修剪左 delimiter 前的所有空白
	//   -}  修剪右 delimiter 后的所有空白
	// 这样当条件为 false 时，不会留下多余空格
	leftTrim := cfg.LeftDelim + "- "
	rightTrim := " -" + cfg.RightDelim

	result := leftTrim + "if " + fieldName + cfg.RightDelim +
		" " + strings.TrimSpace(innerProcessed) +
		cfg.LeftDelim + "end" + rightTrim

	return result, bracketEnd - start
}

// findMatchingBracket 找到匹配的方括号位置
func findMatchingBracket(sql string, from int) int {
	depth := 0
	i := from
	n := len(sql)

	for i < n {
		if sql[i] == '[' {
			depth++
		} else if sql[i] == ']' {
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// extractFieldFromCondition 从条件中提取字段名
// 支持多种格式：AND id = ?, AND id = @id, AND id = {id}, user_name like ? 等
// 关键：如果条件里有模板引用 {.Xxx} / {like .Xxx} / {in .Xxx}，优先从模板里提取
func extractFieldFromCondition(condition string, cfg Config) string {
	condition = strings.TrimSpace(condition)

	// 去掉开头的 AND/OR/WHERE 等关键字
	prefixes := []string{"AND ", "and ", "OR ", "or ", "WHERE ", "where "}
	for _, prefix := range prefixes {
		if strings.HasPrefix(condition, prefix) {
			condition = strings.TrimSpace(condition[len(prefix):])
			break
		}
	}

	// 优先从模板引用里提取：{like .Name} / {in .Ids} / {param .Xxx} / {.Xxx}
	// 这样 if 检查用的字段和模板参数一致
	if idx := strings.Index(condition, "{"); idx >= 0 {
		if endIdx := strings.Index(condition[idx:], "}"); endIdx > 0 {
			tmplContent := condition[idx+1 : idx+endIdx]
			// 从模板内容里提取 .FieldName
			if dotIdx := strings.Index(tmplContent, "."); dotIdx >= 0 {
				rest := tmplContent[dotIdx+1:]
				var fieldBuilder strings.Builder
				for _, r := range rest {
					if unicode.IsLetter(r) || r == '_' || unicode.IsDigit(r) {
						fieldBuilder.WriteRune(r)
					} else {
						break
					}
				}
				if fieldBuilder.Len() > 0 {
					return "." + fieldBuilder.String()
				}
			}
		}
	}

	// 回退：提取 SQL 条件里的第一个标识符
	var fieldBuilder strings.Builder
	for _, r := range condition {
		if unicode.IsLetter(r) || r == '_' {
			fieldBuilder.WriteRune(r)
		} else {
			break
		}
	}

	if fieldBuilder.Len() == 0 {
		return ""
	}

	return "." + fieldBuilder.String()
}

// extractFirstWord 提取第一个英文单词
func extractFirstWord(s string) string {
	s = strings.TrimSpace(s)
	var word strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || r == '_' || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else if word.Len() > 0 {
			break
		}
	}
	return word.String()
}

// peekTemplateFuncName 在模板内容开头 peek 出函数名
// 例如 "{set \"u\" .user}" → "set", "{param .Id .Name}" → "param", "{if .Id}" → ""
// 返回空字符串表示不是函数调用或无法识别
func peekTemplateFuncName(sql string, leftPos int, cfg Config) string {
	leftLen := len(cfg.LeftDelim)
	contentStart := leftPos + leftLen

	// 找到匹配的右分隔符
	end := findMatchingDelim(sql, contentStart, cfg)
	if end == -1 {
		return ""
	}

	content := strings.TrimSpace(sql[contentStart:end])

	// 跳过 Go template whitespace trim 标记 {- / -}
	if strings.HasPrefix(content, "- ") {
		content = strings.TrimPrefix(content, "- ")
		content = strings.TrimSpace(content)
	}
	if strings.HasSuffix(content, " -") {
		content = strings.TrimSuffix(content, " -")
		content = strings.TrimSpace(content)
	}

	// 取第一个 token 作为函数名
	var firstToken strings.Builder
	for _, r := range content {
		if unicode.IsLetter(r) || r == '_' || unicode.IsDigit(r) {
			firstToken.WriteRune(r)
		} else {
			break
		}
	}
	name := strings.ToLower(firstToken.String())

	// 必须是已知的模板函数名
	if _, ok := templateFunctions[name]; ok {
		return name
	}
	return ""
}

// scanBackwardKeyword 从 pos 往回扫描（跳过空白、非 identifier 字符），
// 返回 pos 之前**最后一个** identifier 是否在 keywords 中。
// 例如 "SELECT * FROM user WHERE 1=1 {set .u}" 中 pos='{' 的结果：
//
//	依次看到 '1' → 不是关键字
//	继续看到 'WHERE' → 是 → 返回 true
func scanBackwardKeyword(sql string, pos int, keywords ...string) bool {
	// SQL 子句边界 — 遇到这些表示真的已经跨了一个子句
	clauseBoundary := map[string]bool{
		"SELECT": true, "FROM": true, "WHERE": true, "SET": true,
		"JOIN": true, "LEFT": true, "RIGHT": true, "INNER": true, "OUTER": true,
		"ON": true, "GROUP": true, "BY": true, "ORDER": true, "HAVING": true,
		"LIMIT": true, "OFFSET": true, "UPDATE": true, "INSERT": true,
		"INTO": true, "VALUES": true, "DELETE": true, "CREATE": true,
		"TABLE": true, "ALTER": true, "DROP": true, "UNION": true,
		"INTERSECT": true, "EXCEPT": true, "CASE": true,
	}

	i := pos - 1
	for i >= 0 {
		// 跳过非 identifier 字符
		for i >= 0 {
			c := sql[i]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
				c == '=' || c == '(' || c == ')' || c == ',' || c == '*' ||
				c == '<' || c == '>' || c == '!' || c == '+' || c == '-' ||
				c == '/' || c == '%' || c == '.' {
				i--
				continue
			}
			break
		}
		if i < 0 {
			return false
		}

		// 扫一个 identifier
		end := i + 1
		for i >= 0 {
			r := rune(sql[i])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
				break
			}
			i--
		}
		start := i + 1
		if start >= end {
			return false
		}

		ident := strings.ToUpper(sql[start:end])

		// 命中目标关键字
		for _, kw := range keywords {
			if ident == strings.ToUpper(kw) {
				return true
			}
		}

		// 子句边界（WHERE/FROM/SET 等）但不匹配 → 停住
		// AND/OR 不是边界，继续往回找
		if clauseBoundary[ident] {
			return false
		}
	}
	return false
}

// precededByKeyword 检查 sql[0:pos] 中，紧贴 pos 之前的**最后一个 identifier**
// 是否在 keywords 中。不跨 identifier 扫描。
// 用于判断 "WHERE AND {where .u}" 这种场景下紧挨着的是 AND 还是别的。
func precededByKeyword(sql string, pos int, keywords ...string) bool {
	i := pos - 1

	for i >= 0 {
		c := sql[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i--
			continue
		}
		break
	}
	if i < 0 {
		return false
	}

	end := i + 1
	for i >= 0 {
		r := rune(sql[i])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		i--
	}
	start := i + 1

	if start >= end {
		return false
	}

	ident := strings.ToUpper(sql[start:end])
	for _, kw := range keywords {
		if ident == strings.ToUpper(kw) {
			return true
		}
	}
	return false
}
