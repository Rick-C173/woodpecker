package crypto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("s3cret-pw")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(h, "$argon2id$v=19$"), "PHC 格式前缀: %s", h[:20])

	ok, err := VerifyPassword("s3cret-pw", h)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = VerifyPassword("wrong-pw", h)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHashUniqueSalt(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	assert.NotEqual(t, h1, h2, "同明文两次哈希应产生不同盐")
}

func TestVerifyMalformed(t *testing.T) {
	ok, err := VerifyPassword("x", "not-a-hash")
	assert.Error(t, err)
	assert.False(t, ok)
}

func TestRandomPassword(t *testing.T) {
	p1, err := RandomPassword(12)
	require.NoError(t, err)
	p2, _ := RandomPassword(12)
	assert.Len(t, p1, 12)
	assert.NotEqual(t, p1, p2)
	assert.NotContains(t, p1, "0")
	assert.NotContains(t, p1, "O")
}
