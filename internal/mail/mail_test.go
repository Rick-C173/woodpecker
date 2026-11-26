// mail 模块单测（FR-16）：token 哈希、RFC 5322 报文构造、发件人地址解析、
// 配置有效性、密码加解密落库。SMTP 真实网络发送不做单测（IT 用不可达主机验证失败路径）。
package mail

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/crypto"
)

func TestHashToken(t *testing.T) {
	h1 := HashToken("token-abc")
	h2 := HashToken("token-abc")
	h3 := HashToken("token-abd")

	// 确定性：同 token 同哈希
	assert.Equal(t, h1, h2)
	// 不同 token 不同哈希
	assert.NotEqual(t, h1, h3)
	// sha256 hex：64 位十六进制
	assert.Len(t, h1, 64)
	assert.Regexp(t, `^[0-9a-f]{64}$`, h1)
	// 落库形式不含明文（安全属性：库泄露不泄露 token）
	assert.NotContains(t, h1, "token-abc")
}

func TestBuildMessage(t *testing.T) {
	msg := buildMessage("Woodpecker <noreply@corp.com>", "alice@corp.com",
		"知识库邀请", "点击链接完成注册")

	lines := strings.Split(msg, "\r\n")
	require.Len(t, lines, 8) // 6 头部 + 1 空行 + 1 正文
	assert.Equal(t, "From: Woodpecker <noreply@corp.com>", lines[0])
	assert.Equal(t, "To: alice@corp.com", lines[1])
	// RFC 2047 B 编码主题可解码回中文
	require.True(t, strings.HasPrefix(lines[2], "Subject: =?UTF-8?B?"))
	enc := strings.TrimSuffix(strings.TrimPrefix(lines[2], "Subject: =?UTF-8?B?"), "?=")
	dec, err := base64.StdEncoding.DecodeString(enc)
	require.NoError(t, err)
	assert.Equal(t, "知识库邀请", string(dec))
	assert.Equal(t, "MIME-Version: 1.0", lines[3])
	assert.Equal(t, "Content-Type: text/plain; charset=UTF-8", lines[4])
	// 正文 base64 可解码
	dec, err = base64.StdEncoding.DecodeString(lines[7])
	require.NoError(t, err)
	assert.Equal(t, "点击链接完成注册", string(dec))
}

func TestParseFromAddr(t *testing.T) {
	assert.Equal(t, "noreply@corp.com", parseFromAddr("Woodpecker <noreply@corp.com>"))
	assert.Equal(t, "noreply@corp.com", parseFromAddr("<noreply@corp.com>"))
	assert.Equal(t, "noreply@corp.com", parseFromAddr("noreply@corp.com"))
	assert.Equal(t, "noreply@corp.com", parseFromAddr("  noreply@corp.com  "))
}

func TestConfigValid(t *testing.T) {
	assert.False(t, (*Config)(nil).Valid(), "nil 配置无效")
	assert.False(t, (&Config{Port: 587, From: "a@b.c"}).Valid(), "缺 host")
	assert.False(t, (&Config{Host: "smtp.corp.com", From: "a@b.c"}).Valid(), "缺 port")
	assert.False(t, (&Config{Host: "smtp.corp.com", Port: 0, From: "a@b.c"}).Valid(), "port=0")
	assert.False(t, (&Config{Host: "smtp.corp.com", Port: 587}).Valid(), "缺 from")
	assert.True(t, (&Config{Host: "smtp.corp.com", Port: 587, From: "a@b.c"}).Valid())
}

// fakeStore 内存版 settings 存取（实现 Store 接口）。
type fakeStore struct {
	data map[string][]byte
}

func newFakeStore() *fakeStore { return &fakeStore{data: map[string][]byte{}} }

func (f *fakeStore) GetString(_ context.Context, key, def string) (string, error) {
	if v, ok := f.data[key]; ok {
		return string(v), nil
	}
	return def, nil
}

func (f *fakeStore) Set(_ context.Context, key string, value []byte, _ int64) error {
	f.data[key] = value
	return nil
}

func newTestSMTPStore(t *testing.T) (*SMTPSettingStore, *fakeStore) {
	t.Helper()
	cipher, err := crypto.NewAESGCM("mail-test-key")
	require.NoError(t, err)
	store := &SMTPSettingStore{
		Store:   newFakeStore(),
		Encrypt: cipher.Encrypt,
		Decrypt: cipher.Decrypt,
	}
	return store, store.Store.(*fakeStore)
}

func TestSMTPSettingStoreLoadNotConfigured(t *testing.T) {
	store, _ := newTestSMTPStore(t)
	cfg, err := store.Load(context.Background())
	require.ErrorIs(t, err, ErrNotConfigured)
	assert.Nil(t, cfg)
}

func TestSMTPSettingStoreSaveLoadRoundTrip(t *testing.T) {
	store, raw := newTestSMTPStore(t)
	ctx := context.Background()
	err := store.Save(ctx, 7, &Config{Host: "smtp.corp.com", Port: 465, Username: "noreply@corp.com", From: "Woodpecker <noreply@corp.com>"}, "s3cret!")
	require.NoError(t, err)

	cfg, err := store.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, "smtp.corp.com", cfg.Host)
	assert.Equal(t, 465, cfg.Port)
	assert.True(t, cfg.Valid())

	// 密码密文落库（非明文）
	require.NotEmpty(t, raw.data[settingKey])
	assert.NotContains(t, string(raw.data[settingKey]), "s3cret!")

	// Secret 解密回明文
	pw, err := cfg.Secret(store.Decrypt)
	require.NoError(t, err)
	assert.Equal(t, "s3cret!", pw)
}

func TestSMTPSettingStoreSaveEmptyPasswordKeepsOld(t *testing.T) {
	store, _ := newTestSMTPStore(t)
	ctx := context.Background()
	require.NoError(t, store.Save(ctx, 1, &Config{Host: "smtp.corp.com", Port: 587}, "first-pw"))

	// password 为空 = 不修改（改 host 不改密码）
	require.NoError(t, store.Save(ctx, 1, &Config{Host: "smtp2.corp.com", Port: 587}, ""))
	cfg, err := store.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, "smtp2.corp.com", cfg.Host)
	pw, serr := cfg.Secret(store.Decrypt)
	require.NoError(t, serr)
	assert.Equal(t, "first-pw", pw)
}

func TestConfigSecretErrors(t *testing.T) {
	cipher, err := crypto.NewAESGCM("mail-test-key")
	require.NoError(t, err)

	// 未配置密码 → 空串无错
	empty := &Config{Host: "h", Port: 1, From: "f"}
	pw, err := empty.Secret(cipher.Decrypt)
	require.NoError(t, err)
	assert.Empty(t, pw)

	// 坏 base64 → 明确错误
	bad := &Config{Host: "h", Port: 1, From: "f", PasswordEnc: "!!!not-base64!!!"}
	_, err = bad.Secret(cipher.Decrypt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "密文格式错误")

	// 密钥不匹配 → 解密失败
	other, err := crypto.NewAESGCM("another-key")
	require.NoError(t, err)
	enc, err := other.Encrypt([]byte("pw"))
	require.NoError(t, err)
	mismatch := &Config{Host: "h", Port: 1, From: "f", PasswordEnc: base64.StdEncoding.EncodeToString(enc)}
	_, err = mismatch.Secret(cipher.Decrypt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "解密失败")
}

// 编译期断言：fakeStore 满足 Store 接口。
var _ Store = (*fakeStore)(nil)

func TestSenderConfigured(t *testing.T) {
	store, _ := newTestSMTPStore(t)
	s := NewSMTPSender(store)
	assert.False(t, s.Configured(), "未配置时 Configured=false")

	require.NoError(t, store.Save(context.Background(), 1,
		&Config{Host: "smtp.corp.com", Port: 587, From: "a@b.c"}, ""))
	assert.True(t, s.Configured())
}

func TestSenderSendNotConfigured(t *testing.T) {
	store, _ := newTestSMTPStore(t)
	s := NewSMTPSender(store)
	err := s.Send(context.Background(), "a@b.c", "s", "b")
	require.ErrorIs(t, err, ErrNotConfigured)
}
