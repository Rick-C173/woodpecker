package probe_test

// P2-4 回归探针（审计 2026-11-24，原为 /tmp 下的缺陷复现用例，修复后固化为回归测试）：
// 确认 FetchGit 把私有仓库 token 放在 Basic 认证的哪一侧。
// 用一个假的 git-http 服务捕获 Authorization 头。
//
// 断言写的是**正确行为**：GitLab PAT 要求 `oauth2:<token>`（GitHub/Bitbucket 亦接受），
// 把 token 当用户名、密码留空会让 GitLab 必 401 → 表现为 DOC_003。

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"woodpecker/internal/rag"
)

func TestProbeGitCredentialPlacement(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	_, err := rag.FetchGit(context.Background(), ts.URL+"/repo.git", "", "MYTOKEN123",
		1<<30, 50000, nil, rag.ExclusionRules{})
	t.Logf("【FetchGit 返回】err = %v", err)
	t.Logf("【服务端收到的 Authorization】%q", gotAuth)

	if gotAuth == "" {
		t.Fatalf("没收到 Authorization 头，无法判定")
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimPrefix(gotAuth, "Basic "))
	if derr != nil {
		t.Fatalf("解码失败: %v", derr)
	}
	t.Logf("【解码后 user:pass】%q", string(raw))

	switch string(raw) {
	case "MYTOKEN123:":
		t.Errorf("回归：凭证被放在 username 位置、密码为空 → 发送 %q。"+
			"GitHub/Bitbucket 接受这种形式；GitLab PAT 要求 oauth2:<token>，会 401（DOC_003）", string(raw))
	case ":MYTOKEN123", "oauth2:MYTOKEN123":
		t.Logf("✅ 凭证位置符合平台约定：%q", string(raw))
	default:
		t.Errorf("凭证格式为 %q（既非 token-as-password，也非 oauth2:<token>），GitLab 会 401", string(raw))
	}
}
