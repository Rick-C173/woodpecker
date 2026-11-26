//go:build integration

// M10 集成测试（FR-16 邮箱邀请制）：真实 PG + 全链路 HTTP。
// 覆盖：批量去重/大小写归一、token 哈希落库、公开校验、持邀注册（普通用户）、
// 邮箱登录、一次性/过期/撤销/重发边界、用户名冲突不消耗邀请、SMTP 配置读写
// （密码密文落库/不回显/空密码保留）、测试邮件失败路径（EMAIL_001）、审计写入。
package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/crypto"
	"woodpecker/internal/mail"
	"woodpecker/internal/repo"
)

// inviteByToken 从邀请响应的 link 提取明文 token。
func inviteByToken(t *testing.T, link string) string {
	t.Helper()
	const prefix = "http://it.test/register?invite="
	require.True(t, strings.HasPrefix(link, prefix), "邀请链接前缀应为 APP_BASE_URL: %q", link)
	return strings.TrimPrefix(link, prefix)
}

func TestM10InviteAcceptance(t *testing.T) {
	e := setup(t)

	// 1. bootstrap：首个注册管理员
	sc, sb := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, sb)
	rootToken, _ := sb["token"].(string)
	require.NotEmpty(t, rootToken)

	// 2. 非管理员不能邀请 → 403
	sc, sb = e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "plain", "password": "password8"})
	require.Equal(t, 403, sc) // 注册已关闭
	sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "plain"})
	require.Equal(t, 201, sc, sb)
	plainToken := e.loginReady(t, "plain", sb["initial_password"].(string), "plainpass8")
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", plainToken, map[string]any{"emails": []string{"x@corp.com"}})
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", code(t, sb))

	// 3. 批量邀请：去重 + 大小写归一（Alice@/ALICE@/alice@ → 1 条）；SMTP 未配置降级
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken,
		map[string]any{"emails": []string{"Alice@Corp.com", "ALICE@corp.com", "bob@corp.com"}})
	require.Equal(t, 200, sc, sb)
	results, _ := sb["results"].([]any)
	require.Len(t, results, 2, "重复邮箱应去重")
	r0, _ := results[0].(map[string]any)
	assert.Equal(t, "alice@corp.com", r0["email"])
	assert.Equal(t, true, r0["ok"])
	assert.Equal(t, false, r0["sent"], "SMTP 未配置 → 降级")
	link, _ := r0["link"].(string)
	require.NotEmpty(t, link, "降级路径必须返回明文链接")
	token := inviteByToken(t, link)

	// 4. token 哈希落库（明文不出现在库中）
	assert.Equal(t, int64(1), e.count(t, "SELECT count(*) FROM invites WHERE email = 'alice@corp.com'"))
	assert.Equal(t, int64(0), e.count(t, "SELECT count(*) FROM invites WHERE token_hash = '"+token+"'"))
	assert.Equal(t, int64(1), e.count(t, "SELECT count(*) FROM invites WHERE token_hash = '"+mail.HashToken(token)+"'"))

	// 5. 公开校验：返回邮箱（小写）与过期时间
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+token, "", nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, "alice@corp.com", sb["email"])
	_, ok := sb["expires_at"]
	assert.True(t, ok)

	// 6. 持邀注册：一律普通用户
	sc, sb = e.call(t, "POST", "/api/v1/auth/register/invited", "",
		map[string]any{"token": token, "username": "alice", "password": "alicepass8"})
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, false, sb["is_admin"], sb)
	assert.Equal(t, "alice", sb["username"])
	aliceToken, _ := sb["token"].(string)
	require.NotEmpty(t, aliceToken)
	assert.Equal(t, int64(1), e.count(t, "SELECT count(*) FROM invites WHERE status = 2"), "注册成功即作废（bob 仍 pending）")

	// 7. 邮箱登录（用户名与邮箱混用、大小写不敏感）
	sc, sb = e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "ALICE@corp.com", "password": "alicepass8"})
	require.Equal(t, 200, sc, sb)
	_, ok = sb["token"]
	assert.True(t, ok)

	// 8. 邀请一次性：重复使用 → 409 AUTH_009（校验与注册两侧）
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+token, "", nil)
	assert.Equal(t, 409, sc)
	assert.Equal(t, "AUTH_009", code(t, sb))
	sc, sb = e.call(t, "POST", "/api/v1/auth/register/invited", "",
		map[string]any{"token": token, "username": "alice2", "password": "alicepass8"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "AUTH_009", code(t, sb))

	// 9. 已注册邮箱拒绝邀请（不落库、不消耗）
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken, map[string]any{"emails": []string{"alice@corp.com"}})
	require.Equal(t, 200, sc, sb)
	results, _ = sb["results"].([]any)
	r0, _ = results[0].(map[string]any)
	assert.Equal(t, false, r0["ok"])
	assert.Contains(t, r0["error"], "已注册")

	// 10. 重新邀请（未注册邮箱）→ Upsert 覆盖旧 token
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken, map[string]any{"emails": []string{"bob@corp.com"}})
	require.Equal(t, 200, sc, sb)
	results, _ = sb["results"].([]any)
	r0, _ = results[0].(map[string]any)
	tokenB1 := inviteByToken(t, r0["link"].(string))
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken, map[string]any{"emails": []string{"bob@corp.com"}})
	require.Equal(t, 200, sc, sb)
	results, _ = sb["results"].([]any)
	r0, _ = results[0].(map[string]any)
	tokenB2 := inviteByToken(t, r0["link"].(string))
	require.Equal(t, int64(1), e.count(t, "SELECT count(*) FROM invites WHERE email = 'bob@corp.com'"), "每邮箱一条")
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+tokenB1, "", nil)
	assert.Equal(t, 410, sc)
	assert.Equal(t, "AUTH_008", code(t, sb))
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+tokenB2, "", nil)
	assert.Equal(t, 200, sc, sb)

	// 11. 持邀注册用户名冲突 → USER_001，邀请不被消耗（可重新注册）
	sc, sb = e.call(t, "POST", "/api/v1/auth/register/invited", "",
		map[string]any{"token": tokenB2, "username": "alice", "password": "bobpass88"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "USER_001", code(t, sb))
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+tokenB2, "", nil)
	assert.Equal(t, 200, sc, sb, "用户名冲突不应消耗邀请")
	sc, sb = e.call(t, "POST", "/api/v1/auth/register/invited", "",
		map[string]any{"token": tokenB2, "username": "bob", "password": "bobpass88"})
	require.Equal(t, 200, sc, sb)

	// 12. 校验失败路径：无效格式 422 / 批量上限 422
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken, map[string]any{"emails": []string{"not-an-email"}})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", code(t, sb))
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken,
		map[string]any{"emails": fiftyOne()})
	assert.Equal(t, 422, sc, "单次 >50 应拒绝")

	// 13. 过期邀请：DB 直接置过期 → 410 AUTH_008
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken, map[string]any{"emails": []string{"carol@corp.com"}})
	require.Equal(t, 200, sc, sb)
	results, _ = sb["results"].([]any)
	r0, _ = results[0].(map[string]any)
	tokenC := inviteByToken(t, r0["link"].(string))
	carolInviteID := int64(r0["invite"].(map[string]any)["id"].(float64))
	require.NoError(t, e.db.Exec("UPDATE invites SET expires_at = ? WHERE email = 'carol@corp.com'",
		time.Now().Add(-time.Hour)).Error)
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+tokenC, "", nil)
	assert.Equal(t, 410, sc)
	assert.Equal(t, "AUTH_008", code(t, sb))
	sc, sb = e.call(t, "POST", "/api/v1/auth/register/invited", "",
		map[string]any{"token": tokenC, "username": "carol", "password": "carolpass8"})
	assert.Equal(t, 410, sc)
	assert.Equal(t, "AUTH_008", code(t, sb))

	// 14. 重发已过期邀请 → 覆盖 token、恢复 7 天有效期；旧 token 失效
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations/"+i64(carolInviteID)+"/resend", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	linkC2, _ := sb["link"].(string)
	require.NotEmpty(t, linkC2)
	tokenC2 := inviteByToken(t, linkC2)
	assert.Equal(t, int64(0), e.count(t, "SELECT count(*) FROM invites WHERE id = "+i64(carolInviteID)+" AND expires_at < now()"), "重发应重置有效期")
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+tokenC, "", nil)
	assert.Equal(t, 410, sc, "旧 token 应失效")
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+tokenC2, "", nil)
	assert.Equal(t, 200, sc, sb)

	// 15. 撤销：校验/注册 → 410；重发可复活
	sc, sb = e.call(t, "DELETE", "/api/v1/admin/invitations/"+i64(carolInviteID), rootToken, nil)
	require.Equal(t, 200, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/auth/invitations/"+tokenC2, "", nil)
	assert.Equal(t, 410, sc)
	assert.Equal(t, "AUTH_008", code(t, sb))
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations/"+i64(carolInviteID)+"/resend", rootToken, nil)
	require.Equal(t, 200, sc, sb, "已撤销邀请可重发复活")

	// 16. 已接受邀请：不可重发/撤销（409 AUTH_009）
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations", rootToken, map[string]any{"emails": []string{"dave@corp.com"}})
	require.Equal(t, 200, sc, sb)
	results, _ = sb["results"].([]any)
	r0, _ = results[0].(map[string]any)
	tokenD := inviteByToken(t, r0["link"].(string))
	daveInviteID := int64(r0["invite"].(map[string]any)["id"].(float64))
	sc, sb = e.call(t, "POST", "/api/v1/auth/register/invited", "",
		map[string]any{"token": tokenD, "username": "dave", "password": "davepass8"})
	require.Equal(t, 200, sc, sb)
	sc, sb = e.call(t, "POST", "/api/v1/admin/invitations/"+i64(daveInviteID)+"/resend", rootToken, nil)
	assert.Equal(t, 409, sc)
	assert.Equal(t, "AUTH_009", code(t, sb))
	sc, sb = e.call(t, "DELETE", "/api/v1/admin/invitations/"+i64(daveInviteID), rootToken, nil)
	assert.Equal(t, 409, sc)
	assert.Equal(t, "AUTH_009", code(t, sb))

	// 17. 列表：状态筛选
	sc, sb = e.call(t, "GET", "/api/v1/admin/invitations?status=accepted", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	assert.EqualValues(t, 3, sb["total"], "alice + bob + dave 已接受")
	sc, sb = e.call(t, "GET", "/api/v1/admin/invitations?status=bogus", rootToken, nil)
	assert.Equal(t, 422, sc)

	// 18. 审计写入
	n := e.count(t, "SELECT count(*) FROM audit_logs WHERE action IN ('invite.create','invite.resend','invite.revoke')")
	assert.GreaterOrEqual(t, n, int64(3))

	// 19. 邮箱唯一索引兜底：绕过服务层直接插同邮箱第二个用户 → 唯一冲突
	var aliceEmail *string
	require.NoError(t, e.db.Raw("SELECT email FROM users WHERE username = 'alice'").Scan(&aliceEmail).Error)
	require.NotNil(t, aliceEmail, "持邀注册应写入 email")
	err := e.db.Exec("INSERT INTO users (username, password_hash, is_admin, status, email, must_change_password, created_at, updated_at) VALUES ('dup', 'x', false, 1, $1, false, now(), now())", *aliceEmail).Error
	require.Error(t, err, "users.email 唯一索引应拦截重复")
}

// fiftyOne 构造 51 个不同邮箱（批量上限校验用）。
func fiftyOne() []string {
	emails := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		emails = append(emails, "user"+string(rune('a'+i%26))+string(rune('0'+i/26))+"@corp.com")
	}
	return emails
}

func TestM10EmailSettings(t *testing.T) {
	e := setup(t)

	sc, sb := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, sb)
	rootToken, _ := sb["token"].(string)

	// 1. 未配置：GET configured=false；测试发送 → EMAIL_001（未配置不可发）
	sc, sb = e.call(t, "GET", "/api/v1/admin/email-settings", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, false, sb["configured"])
	sc, sb = e.call(t, "POST", "/api/v1/admin/email-settings/test", rootToken, map[string]any{"to": "x@corp.com"})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "EMAIL_001", code(t, sb))

	// 2. 保存配置：密码密文落库，GET 不回显密码
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/email-settings", rootToken,
		map[string]any{"host": "127.0.0.1", "port": 1, "username": "noreply@corp.com", "password": "smtp-secret", "from": "Woodpecker <noreply@corp.com>"})
	require.Equal(t, 200, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/admin/email-settings", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, "127.0.0.1", sb["host"])
	assert.Equal(t, "noreply@corp.com", sb["username"])
	enc, ok := sb["password_enc"]
	assert.True(t, !ok || enc == "", "密码不得回显")
	assert.Equal(t, int64(0), e.count(t, "SELECT count(*) FROM settings WHERE key = 'email_smtp' AND value::text LIKE '%smtp-secret%'"), "密码必须密文落库")

	// 3. 测试邮件 → 不可达 SMTP → 422 EMAIL_001（真实拨号失败路径）
	sc, sb = e.call(t, "POST", "/api/v1/admin/email-settings/test", rootToken, map[string]any{"to": "x@corp.com"})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "EMAIL_001", code(t, sb))

	// 4. 空 password 更新 → 原密码保留（host 变更生效）
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/email-settings", rootToken,
		map[string]any{"host": "smtp2.corp.com", "port": 587, "username": "noreply@corp.com", "from": "Woodpecker <noreply@corp.com>"})
	require.Equal(t, 200, sc, sb)
	cipher, cerr := crypto.NewAESGCM("it-encryption-key")
	require.NoError(t, cerr)
	store := &mail.SMTPSettingStore{Store: repo.NewSettingRepo(e.db), Encrypt: cipher.Encrypt, Decrypt: cipher.Decrypt}
	cfg, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "smtp2.corp.com", cfg.Host)
	pw, serr := cfg.Secret(cipher.Decrypt)
	require.NoError(t, serr)
	assert.Equal(t, "smtp-secret", pw, "空 password 不得清空已存密码")

	// 5. 非管理员读写配置 → 403
	sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "plain"})
	require.Equal(t, 201, sc, sb)
	plainToken := e.loginReady(t, "plain", sb["initial_password"].(string), "plainpass8")
	sc, sb = e.call(t, "GET", "/api/v1/admin/email-settings", plainToken, nil)
	assert.Equal(t, 403, sc)
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/email-settings", plainToken,
		map[string]any{"host": "evil.corp.com", "port": 25, "from": "x"})
	assert.Equal(t, 403, sc)
}
