//go:build integration

// M13 集成测试（FR-19 SSO / OIDC）：httptest 伪造 IdP（discovery/token/userinfo/jwks），
// 驱动完整 Authorization Code 流：
//   - OIDC 配置保存/连接测试/状态端点
//   - JIT 建号（auth_source=oidc、普通用户）+ 审计
//   - 组自动同步：claim 自动组自动创建、双向对齐（移出不在 claim 的自动组）
//   - 绑定已有同邮箱账号（角色不变）
//   - 管理员映射可选生效
//   - 禁用账号拒绝、state 校验失败拒绝
package integration_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cryptoHashSHA256 PKCS1v15 签名所需的哈希标识。
var cryptoHashSHA256 = crypto.SHA256

func urlQueryUnescape(s string) (string, error) { return url.QueryUnescape(s) }

// ---- mock IdP ----

type mockIdP struct {
	ts          *httptest.Server
	key         *rsa.PrivateKey
	kid         string
	userInfo    atomic.Value // map[string]any：当前登录用户的 claims（每个 code 对应）
	accessToken string
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	m := &mockIdP{key: key, kid: "test-key", accessToken: "mock-access-token"}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                 m.ts.URL,
			"authorization_endpoint": m.ts.URL + "/authorize",
			"token_endpoint":         m.ts.URL + "/token",
			"userinfo_endpoint":      m.ts.URL + "/userinfo",
			"jwks_uri":               m.ts.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": m.kid, "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": "AQAB", "alg": "RS256", "use": "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "authorization_code", r.FormValue("grant_type"))
		// oauth2 库默认以 HTTP Basic 头传递 client_id/client_secret
		clientID := r.FormValue("client_id")
		if user, _, ok := r.BasicAuth(); ok {
			clientID = user
		}
		assert.Equal(t, "wp-client", clientID)
		require.NotEmpty(t, r.FormValue("code_verifier"), "PKCE verifier 必须随兑换提交")
		now := time.Now().Unix()
		nonce := m.userInfo.Load().(map[string]any)["nonce"].(string)
		idToken := m.signIDToken(map[string]any{
			"iss": m.ts.URL, "sub": "user-sub-1", "aud": "wp-client", "nonce": nonce,
			"exp": now + 300, "iat": now,
		})
		writeJSON(w, map[string]any{
			"access_token": m.accessToken, "token_type": "Bearer",
			"id_token": idToken, "expires_in": 300,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+m.accessToken, r.Header.Get("Authorization"))
		writeJSON(w, m.userInfo.Load())
	})

	m.ts = httptest.NewServer(mux)
	t.Cleanup(m.ts.Close)
	return m
}

// signIDToken RS256 签发 id_token（JWKS 提供公钥）。
func (m *mockIdP) signIDToken(claims map[string]any) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"` + m.kid + `","typ":"JWT"}`))
	p, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(p)
	signingInput := h + "." + payload
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.key, cryptoHashSHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// setClaims 设置下一次登录返回的 userinfo claims（nonce 由登录流注入）。
func (m *mockIdP) setClaims(claims map[string]any, nonce string) {
	claims["nonce"] = nonce
	m.userInfo.Store(claims)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---- 登录流辅助 ----

// oidcLogin 模拟完整浏览器流：login 302 → callback（带 cookie/state）→ 提取一次性 code。
func (e *env) oidcLogin(t *testing.T, claims map[string]any, idp *mockIdP) (int, map[string]any, string) {
	t.Helper()

	// 1. 发起登录（捕获 302 Location 与 flow cookie）
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/auth/oidc/login", nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode, "login 应 302 至 IdP")
	location := resp.Header.Get("Location")
	require.Contains(t, location, "/authorize", "应跳转 IdP 授权端点")
	require.Contains(t, location, "code_challenge", "应携带 PKCE")
	state := extractQuery(location, "state")
	require.NotEmpty(t, state)
	var flowCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "oidc_flow" {
			flowCookie = c
		}
	}
	require.NotNil(t, flowCookie, "应下发 oidc_flow cookie")

	// 2. IdP 侧：userinfo claims 就绪（nonce 从 authorize URL 提取）
	nonce := extractQuery(location, "nonce")
	idp.setClaims(claims, nonce)

	// 3. 回调（带 cookie 与 state；code 固定，mock /token 不校验 code）
	cb, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/auth/oidc/callback?code=fixed-code&state="+state, nil)
	cb.AddCookie(flowCookie)
	resp2, err := client.Do(cb)
	require.NoError(t, err)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		return resp2.StatusCode, nil, ""
	}
	target := resp2.Header.Get("Location")
	if strings.Contains(target, "oidc_error") {
		return resp2.StatusCode, map[string]any{"oidc_error": extractQuery(target, "oidc_error")}, ""
	}
	require.Contains(t, target, "?oidc=")
	oneTime := extractQuery(target, "oidc")
	require.NotEmpty(t, oneTime)

	// 4. 换系统 JWT
	sc, body := e.call(t, "POST", "/api/v1/auth/oidc/token", "", map[string]any{"code": oneTime})
	return sc, body, oneTime
}

func extractQuery(rawURL, key string) string {
	i := strings.Index(rawURL, "?")
	if i < 0 {
		return ""
	}
	for _, kv := range strings.Split(rawURL[i+1:], "&") {
		if strings.HasPrefix(kv, key+"=") {
			v, _ := urlQueryUnescape(kv[len(key)+1:])
			return v
		}
	}
	return ""
}

func TestM13OIDC(t *testing.T) {
	e := setup(t)
	idp := newMockIdP(t)

	// 0. 未配置：status=false；login 404
	sc, sb := e.call(t, "GET", "/api/v1/auth/oidc/status", "", nil)
	require.Equal(t, 200, sc)
	assert.Equal(t, false, sb["enabled"])
	sc, sb = e.call(t, "GET", "/api/v1/auth/oidc/login", "", nil)
	assert.Equal(t, 404, sc, "未启用时 login 404")

	// 1. bootstrap 管理员
	sc, sb = e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, sb)
	rootToken, _ := sb["token"].(string)

	// 2. 保存 OIDC 配置（指向 mock IdP）
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/oidc-settings", rootToken, map[string]any{
		"enabled": true, "issuer": idp.ts.URL, "client_id": "wp-client", "client_secret": "wp-secret",
		"redirect_uri": e.ts.URL + "/api/v1/auth/oidc/callback", "frontend_uri": "http://wp.test",
		"groups_claim": "groups", "admin_claim": "", "admin_claim_value": "",
	})
	require.Equal(t, 200, sc, sb)
	// secret 不回显
	sc, sb = e.call(t, "GET", "/api/v1/admin/oidc-settings", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	enc := sb["client_secret_enc"]
	assert.True(t, enc == nil || enc == "", "secret 不得回显")
	assert.Equal(t, true, sb["enabled"])

	// 3. 连接测试
	sc, sb = e.call(t, "POST", "/api/v1/admin/oidc-settings/test", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, idp.ts.URL, sb["issuer"])
	assert.Contains(t, sb["token_endpoint"], "/token")

	// 4. status=true
	sc, sb = e.call(t, "GET", "/api/v1/auth/oidc/status", "", nil)
	assert.Equal(t, true, sb["enabled"])

	// 5. 首次登录：JIT 建号（普通用户）+ 组自动创建并加入
	sc, body, _ := e.oidcLogin(t, map[string]any{
		"email": "alice@corp.com", "preferred_username": "Alice",
		"groups": []any{"后端组", "平台组"},
	}, idp)
	require.Equal(t, 200, sc, body)
	require.NotEmpty(t, body["token"], body)
	assert.Equal(t, "alice", body["username"], "preferred_username 净化小写")
	assert.Equal(t, false, body["is_admin"], "管理员映射未配置 → 普通用户")
	assert.Equal(t, false, body["must_change_password"])
	// 组同步：两个自动组创建并加入
	n := e.count(t, "SELECT count(*) FROM user_groups WHERE auto = true")
	assert.Equal(t, int64(2), n, "claim 组自动创建")
	n = e.count(t, "SELECT count(*) FROM group_members m JOIN user_groups g ON g.id = m.group_id WHERE g.auto = true AND m.user_id = "+fmt.Sprint(int64(body["user_id"].(float64))))
	assert.Equal(t, int64(2), n, "已加入自动组")
	// 审计
	n = e.count(t, "SELECT count(*) FROM audit_logs WHERE action = 'oidc.user_provisioned'")
	assert.Equal(t, int64(1), n)
	n = e.count(t, "SELECT count(*) FROM audit_logs WHERE action = 'oidc.group_sync'")
	assert.Equal(t, int64(1), n)

	// 6. 二次登录（组变化：后端组保留、平台组移出、新增运维组）→ 双向对齐
	sc, body, _ = e.oidcLogin(t, map[string]any{
		"email": "alice@corp.com", "preferred_username": "Alice",
		"groups": []any{"后端组", "运维组"},
	}, idp)
	require.Equal(t, 200, sc, body)
	uid := int64(body["user_id"].(float64))
	inOps := e.count(t, "SELECT count(*) FROM group_members m JOIN user_groups g ON g.id = m.group_id WHERE g.name = '运维组' AND m.user_id = "+fmt.Sprint(uid))
	assert.Equal(t, int64(1), inOps, "新 claim 组加入")
	inPlat := e.count(t, "SELECT count(*) FROM group_members m JOIN user_groups g ON g.id = m.group_id WHERE g.name = '平台组' AND m.user_id = "+fmt.Sprint(uid))
	assert.Equal(t, int64(0), inPlat, "不在 claim 的自动组移出")
	assert.Equal(t, int64(1), e.count(t, "SELECT count(*) FROM users WHERE username = 'alice' AND auth_source = 2"), "绑定同一账号而非新建")

	// 7. 手动组不参与同步：root 建手动组「手动组」并把 alice 加入 → alice 登录（claim 不含）→ 仍在
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups", rootToken, map[string]any{"name": "手动组"})
	require.Equal(t, 201, sc, sb)
	manualGroup := int64(sb["id"].(float64))
	sc, sb = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "SSO 测试库"})
	require.Equal(t, 201, sc, sb)
	kb := int64(sb["id"].(float64))
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(kb)+"/members", rootToken, map[string]any{"user_id": uid, "role": "member"})
	require.Equal(t, 201, sc, sb)
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups/"+i64(manualGroup)+"/members", rootToken, map[string]any{"user_id": uid})
	require.Equal(t, 201, sc, sb)
	sc, body, _ = e.oidcLogin(t, map[string]any{
		"email": "alice@corp.com", "preferred_username": "Alice", "groups": []any{},
	}, idp)
	require.Equal(t, 200, sc, body)
	inManual := e.count(t, "SELECT count(*) FROM group_members WHERE group_id = "+i64(manualGroup)+" AND user_id = "+fmt.Sprint(uid))
	assert.Equal(t, int64(1), inManual, "手动组成员关系不受同步影响")
	inAuto := e.count(t, "SELECT count(*) FROM group_members m JOIN user_groups g ON g.id = m.group_id WHERE g.auto = true AND m.user_id = "+fmt.Sprint(uid))
	assert.Equal(t, int64(0), inAuto, "claim 为空 → 全部自动组移出")
	// OIDC 换发的 JWT 可正常访问授权库（直授 member 生效）
	aliceToken := body["token"].(string)
	sc, sb = e.call(t, "GET", "/api/v1/kbs", aliceToken, nil)
	require.Equal(t, 200, sc, sb)
	items, _ := sb["items"].([]any)
	require.Len(t, items, 1, "OIDC 登录用户可见直授库")
	item, _ := items[0].(map[string]any)
	assert.Equal(t, "SSO 测试库", item["name"])
	assert.Equal(t, "member", item["my_role"])

	// 8. 绑定已有同邮箱本地账号（角色不变：建管理员）
	sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "bob", "is_admin": true})
	require.Equal(t, 201, sc, sb)
	bobID := int64(sb["id"].(float64))
	require.NoError(t, e.db.Exec("UPDATE users SET email = 'bob@corp.com' WHERE id = ?", bobID).Error)
	sc, body, _ = e.oidcLogin(t, map[string]any{
		"email": "bob@corp.com", "preferred_username": "bob-oidc", "groups": []any{},
	}, idp)
	require.Equal(t, 200, sc, body)
	assert.EqualValues(t, bobID, body["user_id"], "同邮箱绑定既有账号")
	assert.Equal(t, true, body["is_admin"], "既有账号角色不变")
	assert.Equal(t, "bob", body["username"])

	// 9. 管理员映射：重新保存配置（admin_claim=roles）→ 新 JIT 用户 is_admin
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/oidc-settings", rootToken, map[string]any{
		"enabled": true, "issuer": idp.ts.URL, "client_id": "wp-client", "client_secret": "wp-secret",
		"redirect_uri": e.ts.URL + "/api/v1/auth/oidc/callback", "frontend_uri": "http://wp.test",
		"groups_claim": "groups", "admin_claim": "roles", "admin_claim_value": "woodpecker-admin",
	})
	require.Equal(t, 200, sc, sb)
	sc, body, _ = e.oidcLogin(t, map[string]any{
		"email": "carol@corp.com", "preferred_username": "carol",
		"roles": []any{"woodpecker-admin"},
	}, idp)
	require.Equal(t, 200, sc, body)
	assert.Equal(t, true, body["is_admin"], "管理员映射生效")

	// 10. 禁用账号 OIDC 登录 → 拒绝
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/users/"+i64(bobID), rootToken, map[string]any{"status": 2})
	require.Equal(t, 200, sc, sb)
	sc, body, _ = e.oidcLogin(t, map[string]any{
		"email": "bob@corp.com", "preferred_username": "bob", "groups": []any{},
	}, idp)
	require.Equal(t, http.StatusFound, sc, "回调 302 到前端错误页")
	if errVal, ok := body["oidc_error"]; ok {
		assert.Contains(t, errVal, "禁用")
	} else {
		t.Fatalf("应包含禁用错误: %v", body)
	}

	// 11. state 校验失败拒绝（不带 cookie 的回调）
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/v1/auth/oidc/callback?code=x&state=bogus", nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Location"), "oidc_error")
}
