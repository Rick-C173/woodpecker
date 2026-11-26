package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/mail"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

// ---- 测试替身（FR-16）----

type fakeInviteRepo struct {
	mu      sync.Mutex
	byEmail map[string]*model.Invite
	seq     int64
}

func newFakeInviteRepo() *fakeInviteRepo {
	return &fakeInviteRepo{byEmail: map[string]*model.Invite{}}
}

func (f *fakeInviteRepo) GetByEmail(_ context.Context, email string) (*model.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.byEmail[email]; ok {
		cp := *m
		return &cp, nil
	}
	return nil, repo.ErrNotFound
}

func (f *fakeInviteRepo) GetByTokenHash(_ context.Context, hash string) (*model.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.byEmail {
		if m.TokenHash == hash {
			cp := *m
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (f *fakeInviteRepo) GetByID(_ context.Context, id int64) (*model.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.byEmail {
		if m.ID == id {
			cp := *m
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (f *fakeInviteRepo) Upsert(_ context.Context, m *model.Invite) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.byEmail[m.Email]; ok {
		m.ID = existing.ID
		m.CreatedAt = existing.CreatedAt
		m.SendCount = existing.SendCount + m.SendCount
	} else {
		f.seq++
		m.ID = f.seq
		m.CreatedAt = time.Now()
	}
	cp := *m
	f.byEmail[m.Email] = &cp
	return nil
}

func (f *fakeInviteRepo) MarkSent(_ context.Context, id int64, sendStatus int16, sendErr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.byEmail {
		if m.ID == id {
			m.SendStatus = sendStatus
			if sendErr == "" {
				m.SendError = nil
			} else {
				m.SendError = &sendErr
			}
			m.SendCount++
		}
	}
	return nil
}

func (f *fakeInviteRepo) MarkAccepted(_ context.Context, id, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for _, m := range f.byEmail {
		if m.ID == id {
			m.Status = model.InviteStatusAccepted
			m.AcceptedAt = &now
			m.AcceptedBy = &userID
		}
	}
	return nil
}

func (f *fakeInviteRepo) Revoke(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.byEmail {
		if m.ID == id {
			m.Status = model.InviteStatusRevoked
		}
	}
	return nil
}

func (f *fakeInviteRepo) List(_ context.Context, q repo.InviteListQuery) ([]model.Invite, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Invite
	for _, m := range f.byEmail {
		if q.Status == nil || *q.Status == m.Status {
			out = append(out, *m)
		}
	}
	return out, int64(len(out)), nil
}

// fakeMailSender 可配置行为的邮件发送替身。
type fakeMailSender struct {
	mu      sync.Mutex
	sentTo  []string
	sendErr error
}

func (f *fakeMailSender) Send(_ context.Context, to, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sentTo = append(f.sentTo, to)
	return nil
}

func (f *fakeMailSender) Configured() bool { return true }

// fakeMailStore SMTP 配置存储替身。
type fakeMailStore struct {
	mu  sync.Mutex
	raw string
}

func (f *fakeMailStore) GetString(_ context.Context, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.raw, nil
}

func (f *fakeMailStore) Set(_ context.Context, _ string, v []byte, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw = string(v)
	return nil
}

// identityCipher 直通加解密，测试免真实密钥。
func identityEncrypt(p []byte) ([]byte, error) { return []byte("enc:" + string(p)), nil }
func identityDecrypt(c []byte) ([]byte, error) {
	s := string(c)
	if !strings.HasPrefix(s, "enc:") {
		return nil, errors.New("bad ciphertext")
	}
	return []byte(strings.TrimPrefix(s, "enc:")), nil
}

// inviteEnv 邀请测试环境。
type inviteEnv struct {
	*testutil.TestEnv
	invites *fakeInviteRepo
	sender  *fakeMailSender
	store   *fakeMailStore
	svc     *service.InviteService
}

// newInviteEnv configured=true：SMTP 配置有效（走发送路径）。
func newInviteEnv(t *testing.T) *inviteEnv {
	t.Helper()
	base := testutil.NewTestEnv(t, nil)
	invites := newFakeInviteRepo()
	sender := &fakeMailSender{}
	store := &fakeMailStore{}
	smtpStore := &mail.SMTPSettingStore{Store: store, Encrypt: identityEncrypt, Decrypt: identityDecrypt}
	require.NoError(t, store.Set(context.Background(), "email_smtp", []byte(
		`{"host":"smtp.test","port":587,"from":"noreply@test.com","base_url":"https://wp.test"}`), 0))
	svc := service.NewInviteService(invites, base.Users, sender, smtpStore, "https://wp.test", base.AuditSvc)
	base.AuthSvc.SetInvites(invites) // 持邀注册（FR-16）
	return &inviteEnv{TestEnv: base, invites: invites, sender: sender, store: store, svc: svc}
}

// notConfiguredEnv SMTP 未配置（降级路径）。
func notConfiguredEnv(t *testing.T) *inviteEnv {
	e := newInviteEnv(t)
	require.NoError(t, e.store.Set(context.Background(), "email_smtp", []byte(""), 0))
	return e
}

func tokenOf(link string) string {
	i := strings.Index(link, "invite=")
	if i < 0 {
		return ""
	}
	return link[i+len("invite="):]
}

func (e *inviteEnv) invite(t *testing.T, email string) service.InviteResult {
	t.Helper()
	results, err := e.svc.Create(context.Background(), e.rootID(t), []string{email}, "ip")
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0]
}

// rootID 幂等取得管理员 ID（多次 invite 共用一个 bootstrap 用户）。
func (e *inviteEnv) rootID(t *testing.T) int64 {
	t.Helper()
	if _, err := e.Users.GetByUsername(context.Background(), "root"); errors.Is(err, repo.ErrNotFound) {
		e.SeedUser(t, "root", "password8", true)
	}
	u, err := e.Users.GetByUsername(context.Background(), "root")
	require.NoError(t, err)
	return u.ID
}

// ---- 创建邀请（FR-16）----

func TestInviteCreateBatchDedupeAndDegrade(t *testing.T) {
	e := notConfiguredEnv(t)
	actor := e.SeedUser(t, "root", "password8", true).ID

	// 大小写折叠去重 + 降级：SMTP 未配置 → 邀请仍创建，返回可手动转发的链接
	results, err := e.svc.Create(context.Background(), actor,
		[]string{"  Alice@Corp.com ", "alice@corp.com", "bob@corp.io"}, "ip")
	require.NoError(t, err)
	require.Len(t, results, 2, "大小写折叠去重")
	assert.False(t, results[0].Sent)
	assert.NotEmpty(t, results[0].Link)
	assert.Contains(t, results[0].Link, "https://wp.test/register?invite=")
	assert.Equal(t, "not_sent", results[0].Invite.SendStatus)
	assert.Equal(t, "pending", results[0].Invite.Status)
	assert.Equal(t, 1, e.Audit.CountAction("invite.create"))

	// 已注册邮箱拒绝（个体失败不阻断整体）
	taken := "taken@corp.com"
	require.NoError(t, e.Users.Create(context.Background(), &model.User{
		Username: "taken", PasswordHash: "x", Status: model.UserStatusActive, Email: &taken,
	}))
	results2, err := e.svc.Create(context.Background(), actor, []string{"taken@corp.com", "new@corp.io"}, "ip")
	require.NoError(t, err)
	require.Len(t, results2, 2)
	assert.False(t, results2[0].OK)
	assert.Contains(t, results2[0].Error, "该邮箱已注册")
	assert.True(t, results2[1].OK)
}

func TestInviteCreateInvalidEmail(t *testing.T) {
	e := notConfiguredEnv(t)
	actor := e.SeedUser(t, "root", "password8", true).ID

	_, err := e.svc.Create(context.Background(), actor, []string{"not-an-email"}, "ip")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.Create(context.Background(), actor, []string{}, "ip")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code, "空邮箱列表")
	_, err = e.svc.Create(context.Background(), actor, []string{strings.Repeat("a", 300) + "@b.com"}, "ip")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code, "超长邮箱")
}

// ---- 发送路径 ----

func TestInviteSendSuccessAndFailure(t *testing.T) {
	e := newInviteEnv(t)
	e.invite(t, "alice@corp.com")
	assert.Equal(t, []string{"alice@corp.com"}, e.sender.sentTo)

	// 发送失败 → 不阻断创建，标记 failed 且降级返回链接
	e.sender.sendErr = errors.New("connection refused")
	res := e.invite(t, "carol@corp.com")
	assert.False(t, res.Sent)
	assert.Equal(t, "failed", res.Invite.SendStatus)
	assert.Contains(t, res.Error, "邮件发送失败")
	assert.NotEmpty(t, res.Link)
}

// ---- 校验生命周期 ----

func TestInviteValidateLifecycle(t *testing.T) {
	e := notConfiguredEnv(t)
	actor := e.SeedUser(t, "root", "password8", true).ID

	res := e.invite(t, "alice@corp.com")
	token := tokenOf(res.Link)

	// 有效
	email, _, err := e.svc.Validate(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "alice@corp.com", email)

	// 已撤销 → 410
	inv, err := e.invites.GetByEmail(context.Background(), "alice@corp.com")
	require.NoError(t, err)
	require.NoError(t, e.svc.Revoke(context.Background(), actor, inv.ID, "ip"))
	_, _, err = e.svc.Validate(context.Background(), token)
	assert.Equal(t, service.CodeInviteInvalid, svcErr(t, err).Code)

	// 重发拿新 token → 有效；时钟快进越过有效期 → 410
	item, err := e.svc.Resend(context.Background(), actor, inv.ID, "ip")
	require.NoError(t, err)
	newToken := tokenOf(item.Link)
	assert.NotEqual(t, token, newToken, "重发生成新 token")
	_, _, err = e.svc.Validate(context.Background(), newToken)
	require.NoError(t, err)
	e.svc.SetClock(func() time.Time { return time.Now().Add(8 * 24 * time.Hour) })
	_, _, err = e.svc.Validate(context.Background(), newToken)
	assert.Equal(t, service.CodeInviteInvalid, svcErr(t, err).Code, "过期邀请 410")
}

func TestInviteResendAndRevokeState(t *testing.T) {
	e := notConfiguredEnv(t)
	actor := e.SeedUser(t, "root", "password8", true).ID

	res := e.invite(t, "alice@corp.com")
	inv, err := e.invites.GetByEmail(context.Background(), "alice@corp.com")
	require.NoError(t, err)

	item, err := e.svc.Resend(context.Background(), actor, inv.ID, "ip")
	require.NoError(t, err)
	assert.Equal(t, "pending", item.Status)
	assert.Equal(t, "not_sent", item.SendStatus)
	assert.NotEmpty(t, item.Link)
	assert.NotEqual(t, tokenOf(res.Link), tokenOf(item.Link), "重发生成新 token")

	// 已接受后重发/撤销 → 409
	require.NoError(t, e.invites.MarkAccepted(context.Background(), inv.ID, actor))
	_, err = e.svc.Resend(context.Background(), actor, inv.ID, "ip")
	assert.Equal(t, service.CodeInviteUsed, svcErr(t, err).Code)
	err = e.svc.Revoke(context.Background(), actor, inv.ID, "ip")
	assert.Equal(t, service.CodeInviteUsed, svcErr(t, err).Code)
}

// ---- 持邀注册（FR-16）----

func TestAuthRegisterInvited(t *testing.T) {
	e := notConfiguredEnv(t)

	res := e.invite(t, "alice@corp.com")
	token := tokenOf(res.Link)

	// 成功：email 绑定 + 邀请 accepted + 普通用户
	auth, err := e.AuthSvc.RegisterInvited(context.Background(), token, "alice", "password8", "ip")
	require.NoError(t, err)
	assert.False(t, auth.IsAdmin, "邀请注册一律普通用户")
	snapshot := e.Users.Snapshot("alice")
	require.NotNil(t, snapshot.Email)
	assert.Equal(t, "alice@corp.com", *snapshot.Email)
	inv, err := e.invites.GetByEmail(context.Background(), "alice@corp.com")
	require.NoError(t, err)
	assert.Equal(t, model.InviteStatusAccepted, inv.Status)
	require.NotNil(t, inv.AcceptedBy)
	assert.Equal(t, snapshot.ID, *inv.AcceptedBy)

	// 重复使用 → 409
	_, err = e.AuthSvc.RegisterInvited(context.Background(), token, "alice2", "password8", "ip")
	assert.Equal(t, service.CodeInviteUsed, svcErr(t, err).Code)

	// 无效 token → 410
	_, err = e.AuthSvc.RegisterInvited(context.Background(), "deadbeef", "bob", "password8", "ip")
	assert.Equal(t, service.CodeInviteInvalid, svcErr(t, err).Code)

	// 用户名冲突 → USER_001
	res2 := e.invite(t, "bob@corp.com")
	_, err = e.AuthSvc.RegisterInvited(context.Background(), tokenOf(res2.Link), "alice", "password8", "ip")
	assert.Equal(t, service.CodeUserExists, svcErr(t, err).Code)

	// 弱密码 → AUTH_006
	_, err = e.AuthSvc.RegisterInvited(context.Background(), tokenOf(res2.Link), "bob", "short", "ip")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
}

// ---- 登录支持邮箱（FR-16）----

func TestLoginByEmail(t *testing.T) {
	e := notConfiguredEnv(t)
	e.invite(t, "alice@corp.com")
	_, err := e.AuthSvc.RegisterInvited(context.Background(),
		tokenOf(e.lastLink(t, "alice@corp.com")), "alice", "password8", "ip")
	require.NoError(t, err)

	_, err = e.AuthSvc.Login(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err, "用户名登录（回归）")
	_, err = e.AuthSvc.Login(context.Background(), "alice@corp.com", "password8", "ip")
	require.NoError(t, err, "邮箱登录")
	_, err = e.AuthSvc.Login(context.Background(), "ALICE@corp.com", "password8", "ip")
	require.NoError(t, err, "邮箱大小写不敏感")
	_, err = e.AuthSvc.Login(context.Background(), "alice@corp.com", "wrong-pass", "ip")
	assert.Equal(t, service.CodeAuthInvalid, svcErr(t, err).Code)
}

// lastLink 取该邮箱最近一次邀请响应的链接（not_sent 场景链接必返回）。
func (e *inviteEnv) lastLink(t *testing.T, email string) string {
	t.Helper()
	// 直接重新生成一次邀请获取链接（幂等覆盖）
	results, err := e.svc.Create(context.Background(), e.rootID(t), []string{email}, "ip")
	require.NoError(t, err)
	return results[0].Link
}
