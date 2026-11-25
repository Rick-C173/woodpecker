package model

import (
	"database/sql/driver"
	"fmt"
	"strings"
)

// StringArray 映射 PostgreSQL text[] 列。
//
// 为什么不用裸 []string：GORM 经 pgx（database/sql 接口）写入时不会把 []string 自动
// 编码为数组字面量，而是生成 RECORD 复合字面量 `('a','b')`，触发 SQLSTATE 42804
// （「类型为 text[] 但表达式的类型为 record」）——端到端验证抓到的真实缺陷，集成测试
// 因用例未覆盖「非空 ignore_rules 落库」而漏网。提供 Valuer/Scanner 按 PG 数组文本
// 格式（{a,"b c"}）显式转换。JSON 序列化继承 []string 行为，API 契约不变。
type StringArray []string

// Value 实现 driver.Valuer：按 PostgreSQL 数组文本输出格式序列化。
func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	parts := make([]string, len(a))
	for i, s := range a {
		parts[i] = quoteArrayElement(s)
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

// Scan 实现 sql.Scanner：解析 PostgreSQL 返回的数组文本格式。
func (a *StringArray) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case []byte:
		*a = parseArrayText(string(v))
		return nil
	case string:
		*a = parseArrayText(v)
		return nil
	default:
		return fmt.Errorf("StringArray.Scan: 不支持的源类型 %T", src)
	}
}

// quoteArrayElement 空元素或含分隔/特殊字符（逗号、引号、反斜杠、花括号、空白）时
// 用双引号包裹并按 PG 规则转义；纯 ASCII 词元（如 *.tmp、docs/*）裸写。
func quoteArrayElement(s string) string {
	if s != "" && !strings.ContainsAny(s, ",\"{}\\ \t\n") {
		return s
	}
	esc := strings.ReplaceAll(s, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	return `"` + esc + `"`
}

// parseArrayText 解析 {elem,elem} 文本格式；空串 / {} / NULL 视为空切片。
func parseArrayText(s string) StringArray {
	s = strings.TrimSpace(s)
	if s == "" || s == "NULL" {
		return nil
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return StringArray{}
	}
	out := make(StringArray, 0, 4)
	for i := 0; i < len(inner); {
		if inner[i] == '"' {
			// 引号元素：读至闭合引号，处理反斜杠转义
			i++
			var sb strings.Builder
			for i < len(inner) {
				c := inner[i]
				if c == '\\' && i+1 < len(inner) {
					sb.WriteByte(inner[i+1])
					i += 2
					continue
				}
				if c == '"' {
					i++
					break
				}
				sb.WriteByte(c)
				i++
			}
			out = append(out, sb.String())
			if i < len(inner) && inner[i] == ',' {
				i++
			}
			continue
		}
		// 裸元素：读至下一个逗号；NULL 表示空字符串元素
		j := strings.IndexByte(inner[i:], ',')
		var raw string
		if j < 0 {
			raw = inner[i:]
			i = len(inner)
		} else {
			raw = inner[i : i+j]
			i += j + 1
		}
		if raw == "NULL" {
			out = append(out, "")
		} else {
			out = append(out, raw)
		}
	}
	return out
}
