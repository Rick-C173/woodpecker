package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 畸形 PHC 输入的解析分支（语句覆盖）。

func TestVerifyMalformedVariants(t *testing.T) {
	valid, _ := HashPassword("pw")
	cases := []string{
		"not-a-hash",                    // 段数不足
		"$md5$v=19$m=1,t=1,p=1$s$h",     // 非argon2id
		"$argon2id$vx$m=1,t=1,p=1$s$h",  // 版本解析失败
		"$argon2id$v=19$mx,t=1,p=1$s$h", // 参数解析失败
		"$argon2id$v=19$m=1,t=1,p=1$!! badbase64!!$h", // 盐 base64 非法
		"$argon2id$v=19$m=1,t=1,p=1$abcd$!! bad!!",    // 哈希 base64 非法
	}
	for _, in := range cases {
		ok, err := VerifyPassword("pw", in)
		assert.False(t, ok, in)
		assert.Error(t, err, in)
	}

	// 合法格式但错误的哈希值（长度截断路径）
	ok, err := VerifyPassword("pw", valid+"x")
	assert.NoError(t, err)
	assert.False(t, ok)
}
