package model

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStringArrayRoundTrip Value→Scan 往返：普通词元、特殊字符、空数组、nil。
func TestStringArrayRoundTrip(t *testing.T) {
	cases := []StringArray{
		{},
		{".github/*", "*.tmp"},
		{"docs/*", "with space", "comma,inside"},
		{`quote"inside`, `back\slash`},
		{""},              // 单个空元素必须带引号
		{"brace{inside}"}, // 花括号元素
		{"a", "", "b"},    // 中间空元素
		{"中文规则*"},         // 非 ASCII
	}
	for _, in := range cases {
		v, err := in.Value()
		require.NoError(t, err)
		require.NotEmpty(t, v, "非 nil 数组必须产出文本")
		var out StringArray
		require.NoError(t, out.Scan(v))
		assert.Equal(t, []string(in), []string(out), "往返不一致: %v → %s", in, v)
	}

	// nil → NULL → nil
	v, err := StringArray(nil).Value()
	require.NoError(t, err)
	assert.Nil(t, v)
	var n StringArray
	require.NoError(t, n.Scan(nil))
	assert.Nil(t, n)

	// 空数组文本
	var e StringArray
	require.NoError(t, e.Scan("{}"))
	assert.Empty(t, e)
}

// TestStringArrayScanFormats Scan 兼容驱动可能返回的 []byte 与 string 两种形态。
func TestStringArrayScanFormats(t *testing.T) {
	var a StringArray
	require.NoError(t, a.Scan([]byte(`{".github/*","*.tmp"}`)))
	assert.Equal(t, StringArray{".github/*", "*.tmp"}, a)

	var b StringArray
	require.NoError(t, b.Scan(sql.NullString{String: `{a,b}`, Valid: true}.String))
	assert.Equal(t, StringArray{"a", "b"}, b)

	var c StringArray
	err := c.Scan(42)
	require.Error(t, err, "非文本源类型必须报错")
}

// TestStringArrayValueTextFormat Value 产出必须符合 PG 数组文本格式（可被 ParseArrayText 消费）。
func TestStringArrayValueTextFormat(t *testing.T) {
	v, _ := StringArray{"docs/drafts/*", `we"ird\path`, "a b"}.Value()
	assert.Equal(t, `{docs/drafts/*,"we\"ird\\path","a b"}`, v, "含引号/反斜杠/空格的元素必须加引号转义")
	v2, _ := StringArray{"plain", ""}.Value()
	assert.Equal(t, `{plain,""}`, v2, "空元素必须带引号")
}
