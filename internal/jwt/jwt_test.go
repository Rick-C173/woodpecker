package jwt

import (
	"testing"
	"time"

	jwt5 "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

func TestIssueParseRoundtrip(t *testing.T) {
	issuer := NewIssuer("test-secret", time.Hour)
	u := &model.User{ID: 42, IsAdmin: true, TokenVersion: 3}

	token, err := issuer.Issue(u)
	require.NoError(t, err)

	claims, err := issuer.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, int64(42), claims.UserID)
	assert.True(t, claims.IsAdmin)
	assert.Equal(t, 3, claims.TokenVersion)
}

func TestParseWrongSecret(t *testing.T) {
	issuer := NewIssuer("secret-a", time.Hour)
	u := &model.User{ID: 1, TokenVersion: 0}
	token, _ := issuer.Issue(u)

	other := NewIssuer("secret-b", time.Hour)
	_, err := other.Parse(token)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestParseExpired(t *testing.T) {
	issuer := NewIssuer("secret", -time.Minute) // 已过期
	u := &model.User{ID: 1, TokenVersion: 0}
	token, _ := issuer.Issue(u)

	_, err := issuer.Parse(token)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestParseGarbage(t *testing.T) {
	issuer := NewIssuer("secret", time.Hour)
	_, err := issuer.Parse("garbage.token.here")
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestParseWrongAlgorithm(t *testing.T) {
	// HS512 签名的 token 应被拒绝（仅允许 HS256）
	claims := jwt5.MapClaims{"sub": "1", "ver": 0, "exp": time.Now().Add(time.Hour).Unix()}
	tok := jwt5.NewWithClaims(jwt5.SigningMethodHS512, claims)
	signed, err := tok.SignedString([]byte("secret"))
	require.NoError(t, err)

	issuer := NewIssuer("secret", time.Hour)
	_, err = issuer.Parse(signed)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}
