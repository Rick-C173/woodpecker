package crypto_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/crypto"
)

func TestAESGCMRoundtrip(t *testing.T) {
	a, err := crypto.NewAESGCM("dev-only-encryption-key-32bytes!!")
	require.NoError(t, err)
	plain := []byte(`{"kind":"token","secret":"ghp_xxx"}`)
	enc, err := a.Encrypt(plain)
	require.NoError(t, err)
	assert.NotEqual(t, plain, enc)
	dec, err := a.Decrypt(enc)
	require.NoError(t, err)
	assert.Equal(t, plain, dec)

	// 同明文两次加密产生不同 nonce（密文不同）
	enc2, _ := a.Encrypt(plain)
	assert.NotEqual(t, enc, enc2)

	// 空明文
	encE, err := a.Encrypt(nil)
	require.NoError(t, err)
	decE, err := a.Decrypt(encE)
	require.NoError(t, err)
	assert.Empty(t, decE)
}

func TestAESGCMTamperAndWrongKey(t *testing.T) {
	a, err := crypto.NewAESGCM("key-a")
	require.NoError(t, err)
	enc, err := a.Encrypt([]byte("secret-data"))
	require.NoError(t, err)

	// 篡改密文 → ErrCiphertextInvalid
	tampered := append([]byte{}, enc...)
	tampered[len(tampered)-1] ^= 0xFF
	_, err = a.Decrypt(tampered)
	require.ErrorIs(t, err, crypto.ErrCiphertextInvalid)

	// 截断密文 → ErrCiphertextInvalid
	_, err = a.Decrypt(enc[:5])
	require.ErrorIs(t, err, crypto.ErrCiphertextInvalid)

	// 不同密钥解密 → ErrCiphertextInvalid
	b, err := crypto.NewAESGCM("key-b")
	require.NoError(t, err)
	_, err = b.Decrypt(enc)
	assert.ErrorIs(t, err, crypto.ErrCiphertextInvalid)
}

func TestAESGCMDeterministicKey(t *testing.T) {
	// 相同密钥字符串 → 相同 GCM（可互相解密）
	a1, err := crypto.NewAESGCM("same-key")
	require.NoError(t, err)
	a2, err := crypto.NewAESGCM("same-key")
	require.NoError(t, err)
	enc, err := a1.Encrypt([]byte("hello"))
	require.NoError(t, err)
	dec, err := a2.Decrypt(enc)
	require.NoError(t, err)
	assert.True(t, bytes.Equal([]byte("hello"), dec))
}
