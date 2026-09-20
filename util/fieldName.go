package util

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SnakeToCamel 将 snake_case 转换为 camelCase
// "user_name" -> "userName", "create_time" -> "createTime"
func SnakeToCamel(snake string) string {
	if snake == "" {
		return snake
	}
	parts := strings.Split(snake, "_")
	var result strings.Builder
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 {
			// 第一段保持小写
			r, w := utf8.DecodeRuneInString(part)
			result.WriteRune(unicode.ToLower(r))
			result.WriteString(part[w:])
		} else {
			// 后续段首字母大写
			r, w := utf8.DecodeRuneInString(part)
			result.WriteRune(unicode.ToUpper(r))
			result.WriteString(part[w:])
		}
	}
	return result.String()
}

// CamelToPascal 将 camelCase 转换为 PascalCase
// "userName" -> "UserName"
func CamelToPascal(camel string) string {
	if camel == "" {
		return camel
	}
	r, w := utf8.DecodeRuneInString(camel)
	return string(unicode.ToUpper(r)) + camel[w:]
}

// PascalToSnakeCase 将 PascalCase 转换为 snake_case（SnakeToPascalCase 的逆函数）
// "UserName" -> "user_name", "UserId" -> "user_id", "Id" -> "id"
// 处理连续大写： "HTMLParser" -> "html_parser"
func PascalToSnakeCase(pascal string) string {
	if pascal == "" {
		return pascal
	}
	var result strings.Builder
	runes := []rune(pascal)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		if unicode.IsUpper(r) {
			// 在大写字母前插入下划线（不是第一个字符时）
			if i > 0 {
				// 特殊处理连续大写：HTMLParser -> html_parser（P 前不加，因为前面 HTML 都是大写）
				// 但如果下一个是小写，当前这个大写是单词的结束
				if i+1 < n && unicode.IsLower(runes[i+1]) {
					result.WriteByte('_')
				} else {
					// 下一个还是大写，或者是最后一个
					if i > 1 && unicode.IsLower(runes[i-1]) {
						result.WriteByte('_')
					}
				}
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
