package rag

// 凭证与传输层回归（审计 P1-11/P1-12）：
//   - token → HTTPS BasicAuth（token 放密码位，用户名可配 GIT_TOKEN_USERNAME）
//   - ssh → PublicKeys（私钥 PEM 解析，主机指纹走 known_hosts）
//   - 旧数据（加密的裸 token 字符串）解密后 JSON 解析失败 → 按 kind=token 兼容

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	xssh "golang.org/x/crypto/ssh"
)

// testSSHPrivateKeyPEM 生成一把一次性 ed25519 私钥（PEM，未加密）。
func testSSHPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成测试私钥: %v", err)
	}
	block, err := xssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("序列化测试私钥: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestParseCredential(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Credential
	}{
		{"新格式 token", `{"kind":"token","secret":"ghp_x"}`, Credential{Kind: CredentialKindToken, Secret: "ghp_x"}},
		{"新格式 ssh", `{"kind":"ssh","secret":"-----BEGIN PRIVATE KEY-----"}`, Credential{Kind: CredentialKindSSH, Secret: "-----BEGIN PRIVATE KEY-----"}},
		{"旧数据裸字符串", "ghp_legacy", Credential{Kind: CredentialKindToken, Secret: "ghp_legacy"}},
		{"旧数据带空白", "  ghp_legacy \n", Credential{Kind: CredentialKindToken, Secret: "ghp_legacy"}},
		{"空", "", Credential{}},
		{"JSON 但 kind 非法", `{"kind":"basic","secret":"s"}`, Credential{Kind: CredentialKindToken, Secret: `{"kind":"basic","secret":"s"}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCredential([]byte(tc.in)); got != tc.want {
				t.Fatalf("ParseCredential(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsSSHURL(t *testing.T) {
	cases := map[string]bool{
		"git@github.com:org/repo.git": true,
		"ssh://git@github.com/org/r":  true,
		"https://github.com/o/r.git":  false,
		"http://gitlab.local/o/r.git": false,
		"file:///tmp/repo.git":        false,
		"github.com/o/r":              false,
	}
	for in, want := range cases {
		if got := IsSSHURL(in); got != want {
			t.Errorf("IsSSHURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAuthForTokenUsesBasicAuthPasswordSide(t *testing.T) {
	auth, err := authFor("https://gitlab.local/o/r.git", Credential{Kind: CredentialKindToken, Secret: "TOKEN"})
	if err != nil {
		t.Fatalf("authFor: %v", err)
	}
	ba, ok := auth.(*githttp.BasicAuth)
	if !ok {
		t.Fatalf("token 应使用 HTTPS BasicAuth，实际 %T", auth)
	}
	if ba.Username != "oauth2" || ba.Password != "TOKEN" {
		t.Fatalf("token 必须放密码位：got %q:%q", ba.Username, ba.Password)
	}

	// 用户名可通过 GIT_TOKEN_USERNAME 覆盖（GitHub 可用 x-access-token）
	t.Setenv("GIT_TOKEN_USERNAME", "x-access-token")
	auth, err = authFor("https://github.com/o/r.git", Credential{Kind: CredentialKindToken, Secret: "TOKEN"})
	if err != nil {
		t.Fatalf("authFor: %v", err)
	}
	if ba := auth.(*githttp.BasicAuth); ba.Username != "x-access-token" {
		t.Fatalf("GIT_TOKEN_USERNAME 未生效：%q", ba.Username)
	}

	// 兼容：kind 缺省但 secret 非空 → 按 token
	auth, err = authFor("https://github.com/o/r.git", Credential{Secret: "TOKEN"})
	if err != nil {
		t.Fatalf("authFor: %v", err)
	}
	if _, ok := auth.(*githttp.BasicAuth); !ok {
		t.Fatalf("kind 缺省应回落 token，实际 %T", auth)
	}
}

func TestAuthForSSHUsesPublicKeys(t *testing.T) {
	key := testSSHPrivateKeyPEM(t)
	auth, err := authFor("git@github.com:org/repo.git", Credential{Kind: CredentialKindSSH, Secret: key})
	if err != nil {
		t.Fatalf("authFor ssh: %v", err)
	}
	pk, ok := auth.(*gitssh.PublicKeys)
	if !ok {
		t.Fatalf("ssh 凭证应使用 PublicKeys，实际 %T", auth)
	}
	if pk.User != "git" {
		t.Fatalf("scp 形式地址应从 URL 解析用户名 git，实际 %q", pk.User)
	}
	if pk.Signer == nil {
		t.Fatal("Signer 不应为空")
	}
	// 主机指纹校验必须已配置（known_hosts 或显式降级），不能是 nil
	cfg, cerr := pk.ClientConfig()
	if cerr != nil {
		t.Fatalf("ClientConfig: %v", cerr)
	}
	if cfg.HostKeyCallback == nil {
		t.Fatal("HostKeyCallback 不应为 nil")
	}

	// ssh:// 形式的用户名同样从 URL 取
	auth, err = authFor("ssh://deploy@git.internal:2222/repo.git", Credential{Kind: CredentialKindSSH, Secret: key})
	if err != nil {
		t.Fatalf("authFor ssh://: %v", err)
	}
	if pk := auth.(*gitssh.PublicKeys); pk.User != "deploy" {
		t.Fatalf("ssh:// 用户名解析错误：%q", pk.User)
	}
}

func TestAuthForErrors(t *testing.T) {
	// SSH 地址但未提供私钥 → 明确报错（不能被误报成「匿名不可达」）
	if _, err := authFor("git@github.com:o/r.git", Credential{}); err == nil {
		t.Fatal("SSH 地址缺凭证应报错")
	}
	// ssh kind 但私钥不是 PEM
	if _, err := authFor("git@github.com:o/r.git", Credential{Kind: CredentialKindSSH, Secret: "not-a-key"}); err == nil {
		t.Fatal("非 PEM 私钥应报错")
	}
	// 未知 kind
	if _, err := authFor("https://github.com/o/r.git", Credential{Kind: "basic", Secret: "s"}); err == nil {
		t.Fatal("未知 kind 应报错")
	}
	// HTTPS 匿名 → nil auth 且无错误
	auth, err := authFor("https://github.com/o/r.git", Credential{})
	if err != nil || auth != nil {
		t.Fatalf("HTTPS 匿名应为 (nil, nil)，实际 (%v, %v)", auth, err)
	}
}

// TestFetchGitTokenSendsBasicAuth 端到端确认 token 出现在 Basic 认证的密码位。
func TestFetchGitTokenSendsBasicAuth(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	_, err := FetchGit(context.Background(), ts.URL+"/repo.git", "",
		Credential{Kind: CredentialKindToken, Secret: "MYTOKEN"}, 1<<30, 1000, nil, ExclusionRules{})
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimPrefix(gotAuth, "Basic "))
	if derr != nil {
		t.Fatalf("解码 Authorization 失败: %v（原值 %q）", derr, gotAuth)
	}
	if string(raw) != "oauth2:MYTOKEN" {
		t.Fatalf("期望 oauth2:MYTOKEN，实际 %q", string(raw))
	}
}
