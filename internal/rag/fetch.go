package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-enry/go-enry/v2"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	xssh "golang.org/x/crypto/ssh"
)

// 分类错误：service 层映射到 DOC_001/DOC_003 错误码。
var (
	ErrRepoUnreachable = errors.New("仓库地址不可达或凭证无效")
	ErrRepoTooLarge    = errors.New("仓库超过大小或文件数限制")
	ErrURLUnreachable  = errors.New("URL 无法访问")
	ErrURLNotHTML      = errors.New("URL 内容不是 HTML 页面")
)

// 拉取过程限制（R2）。
const (
	maxSingleFileBytes = 5 << 20 // 单文件 5MB（超出跳过并标记）
	gitTimeout         = 10 * time.Minute
	urlTimeout         = 30 * time.Second
)

// skipDirs 跳过的目录（依赖目录、版本控制、构建产物）。
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"target": true, "build": true, "__pycache__": true, ".venv": true, "venv": true,
}

// SourceFile 拉取到的源文件（仅受支持且未排除的文件带内容）。
type SourceFile struct {
	Path    string // 相对路径（/ 分隔）
	Content []byte
}

// FetchResult 拉取结果。
type FetchResult struct {
	Files     []SourceFile
	Excluded  []string // R1b / ignore_rules 命中的相对路径（文件清单标"已排除"）
	Oversized []string // 超过单文件上限的路径（文件清单标"失败"）
	Commit    string   // git HEAD hash；URL 为空
	TotalSize int64    // 受支持文件总大小
	RepoSize  int64    // 遍历到的全部文件大小（R2 上限判定）
	FileSeen  int      // 遍历到的全部文件数（R2 上限判定）
}

// gitTokenUsername 私有仓库 token 认证的用户名位取值（可用 GIT_TOKEN_USERNAME 覆盖）。
// GitLab PAT 要求 `oauth2:<token>`；GitHub / Bitbucket 同样接受该组合，
// 故默认用它覆盖三类平台的 HTTPS 认证（审计 P2-36）。
const defaultGitTokenUsername = "oauth2"

// 凭证类型（与 service.CredentialKind* 保持一致，落库为 {"kind","secret"}）。
const (
	CredentialKindToken = "token" // HTTPS 访问令牌（BasicAuth 密码位）
	CredentialKindSSH   = "ssh"   // SSH 私钥（PEM），PublicKeys 认证
)

// Credential Git 仓库凭证（解密后的明文结构）。
// 零值表示匿名访问；kind 缺省但 secret 非空时按 token 处理（旧客户端/旧数据兼容）。
type Credential struct {
	Kind   string `json:"kind"`
	Secret string `json:"secret"`
}

// ParseCredential 解析解密后的凭证明文（审计 P1-11/P1-12）：
//   - 新数据：{"kind":"token|ssh","secret":"..."}
//   - 旧数据：裸 token 字符串（kind 信息在落库前即丢失）→ 按 kind=token 兼容处理
func ParseCredential(plain []byte) Credential {
	s := strings.TrimSpace(string(plain))
	if s == "" {
		return Credential{}
	}
	var c Credential
	if err := json.Unmarshal([]byte(s), &c); err == nil {
		c.Kind = strings.ToLower(strings.TrimSpace(c.Kind))
		if c.Secret != "" && (c.Kind == CredentialKindToken || c.Kind == CredentialKindSSH) {
			return c
		}
	}
	return Credential{Kind: CredentialKindToken, Secret: s}
}

// gitTokenUsername token 认证的用户名位（GIT_TOKEN_USERNAME 可覆盖，默认 oauth2）。
func gitTokenUsername() string {
	if v := strings.TrimSpace(os.Getenv("GIT_TOKEN_USERNAME")); v != "" {
		return v
	}
	return defaultGitTokenUsername
}

// IsSSHURL 地址是否走 SSH 传输层：ssh:// 前缀，或 scp 形式 user@host:path。
func IsSSHURL(repoURL string) bool {
	if strings.HasPrefix(repoURL, "ssh://") {
		return true
	}
	if strings.Contains(repoURL, "://") {
		return false // http(s)/file 等显式 scheme 不走 SSH
	}
	at := strings.Index(repoURL, "@")
	if at <= 0 {
		return false
	}
	return strings.Contains(repoURL[at+1:], ":")
}

// sshUserFromURL 解析 SSH 地址中的用户名（scp 形式 git@host:path / ssh://git@host/path）。
func sshUserFromURL(repoURL string) string {
	if u, err := url.Parse(repoURL); err == nil && u.User != nil {
		if name := u.User.Username(); name != "" {
			return name
		}
	}
	if at := strings.Index(repoURL, "@"); at > 0 {
		return repoURL[:at]
	}
	return gitssh.DefaultUsername // "git"
}

// knownHostsCallback 优先用 known_hosts 校验主机指纹；文件缺失时退化为不校验并告警一次
// （审计 P1-11：生产应配置 ~/.ssh/known_hosts 或 SSH_KNOWN_HOSTS）。
func knownHostsCallback() xssh.HostKeyCallback {
	cb, err := gitssh.NewKnownHostsCallback()
	if err != nil {
		slog.Warn("known_hosts 不可用，SSH 主机指纹将不做校验；生产环境应配置 ~/.ssh/known_hosts 或 SSH_KNOWN_HOSTS",
			"err", err)
		return xssh.InsecureIgnoreHostKey() //nolint:gosec // 显式降级并已告警，避免无 known_hosts 时完全不可用
	}
	return cb
}

// authFor 按凭证 kind 与地址前缀选择传输层认证（审计 P1-11/P1-12）。
// 返回 nil 表示匿名访问。
func authFor(repoURL string, cred Credential) (transport.AuthMethod, error) {
	kind := strings.ToLower(strings.TrimSpace(cred.Kind))
	secret := strings.TrimSpace(cred.Secret)
	if kind == "" && secret != "" {
		kind = CredentialKindToken // 兼容旧客户端：未带 kind 时按 token
	}
	if secret == "" {
		if IsSSHURL(repoURL) {
			// SSH 必须提供私钥；否则问题会被误报成「仓库不可达」（PRD FR-03 要求凭证）
			return nil, fmt.Errorf("%w: SSH 仓库必须提供 credential{kind:\"ssh\",secret:<PEM 私钥>}",
				ErrRepoUnreachable)
		}
		return nil, nil // HTTPS 匿名
	}
	switch kind {
	case CredentialKindToken:
		// token 必须放**密码位**：GitLab PAT 只认 oauth2:<token>（审计 P2-36）
		return &githttp.BasicAuth{Username: gitTokenUsername(), Password: secret}, nil
	case CredentialKindSSH:
		pub, err := gitssh.NewPublicKeys(sshUserFromURL(repoURL), []byte(secret), "")
		if err != nil {
			return nil, fmt.Errorf("%w: 解析 SSH 私钥失败（需要未加密的 PEM 私钥）: %s",
				ErrRepoUnreachable, err.Error())
		}
		pub.HostKeyCallback = knownHostsCallback()
		return pub, nil
	default:
		return nil, fmt.Errorf("%w: 不支持的凭证类型 %q（token|ssh）", ErrRepoUnreachable, kind)
	}
}

// FetchGit 克隆仓库（浅克隆）并读取受支持文件。
// cred 为零值时匿名拉取；kind=token 走 HTTPS BasicAuth（token 放密码位），
// kind=ssh 走 PublicKeys（私钥 PEM + known_hosts 主机指纹校验）。
func FetchGit(ctx context.Context, repoURL, gitRef string, cred Credential, maxRepoBytes int64, maxFiles int, ignoreRules []string, excl ExclusionRules) (*FetchResult, error) {
	tmp, err := os.MkdirTemp("", "wp-clone-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cloneOpts := &git.CloneOptions{URL: repoURL, Depth: 1, SingleBranch: true}
	if gitRef != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(gitRef)
	}
	auth, aerr := authFor(repoURL, cred)
	if aerr != nil {
		return nil, aerr
	}
	cloneOpts.Auth = auth
	cloneCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	repo, err := git.PlainCloneContext(cloneCtx, tmp, false, cloneOpts)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRepoUnreachable, err.Error())
	}
	res := &FetchResult{}
	if head, herr := repo.Head(); herr == nil {
		res.Commit = head.Hash().String()
	}

	err = filepath.WalkDir(tmp, func(abs string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		rel, rerr := filepath.Rel(tmp, abs)
		if rerr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipDirs[d.Name()] || IsExcluded(rel+"/", ignoreRules, excl) {
				return filepath.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		res.FileSeen++
		res.RepoSize += info.Size()
		if res.RepoSize > maxRepoBytes || res.FileSeen > maxFiles {
			return ErrRepoTooLarge
		}
		if IsExcluded(rel, ignoreRules, excl) {
			res.Excluded = append(res.Excluded, rel)
			return nil
		}
		if !IsSupportedExt(rel) {
			return nil // R1 之外的格式静默跳过（不进清单）
		}
		if info.Size() > maxSingleFileBytes {
			res.Oversized = append(res.Oversized, rel)
			return nil
		}
		content, rerr := os.ReadFile(abs)
		if rerr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		res.Files = append(res.Files, SourceFile{Path: rel, Content: content})
		res.TotalSize += int64(len(content))
		return nil
	})
	if errors.Is(err, ErrRepoTooLarge) {
		return nil, ErrRepoTooLarge
	}
	if err != nil {
		return nil, fmt.Errorf("遍历仓库: %w", err)
	}
	return res, nil
}

// FetchLocalDir 遍历本地目录（上传文件落地目录的 reindex 用）。
func FetchLocalDir(ctx context.Context, root string, maxRepoBytes int64, maxFiles int, ignoreRules []string, excl ExclusionRules) (*FetchResult, error) {
	res := &FetchResult{}
	err := filepath.WalkDir(root, func(abs string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		rel, rerr := filepath.Rel(root, abs)
		if rerr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipDirs[d.Name()] || IsExcluded(rel+"/", ignoreRules, excl) {
				return filepath.SkipDir
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		res.FileSeen++
		res.RepoSize += info.Size()
		if res.RepoSize > maxRepoBytes || res.FileSeen > maxFiles {
			return ErrRepoTooLarge
		}
		if IsExcluded(rel, ignoreRules, excl) {
			res.Excluded = append(res.Excluded, rel)
			return nil
		}
		if !IsSupportedExt(rel) {
			return nil
		}
		if info.Size() > maxSingleFileBytes {
			res.Oversized = append(res.Oversized, rel)
			return nil
		}
		content, rerr := os.ReadFile(abs)
		if rerr != nil {
			return nil //nolint:nilerr // 容错跳过：单个条目失败不中断遍历
		}
		res.Files = append(res.Files, SourceFile{Path: rel, Content: content})
		res.TotalSize += int64(len(content))
		return nil
	})
	if errors.Is(err, ErrRepoTooLarge) {
		return nil, ErrRepoTooLarge
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

// FetchURL 抓取单个页面并提取正文文本（MVP：单页，去导航/脚本/样式）。
func FetchURL(ctx context.Context, rawURL string) (body []byte, title string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", fmt.Errorf("%w: 非法地址", ErrURLUnreachable)
	}
	reqCtx, cancel := context.WithTimeout(ctx, urlTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrURLUnreachable, err.Error())
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Woodpecker/0.1)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrURLUnreachable, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: HTTP %d", ErrURLUnreachable, resp.StatusCode)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if ct != "" && !strings.Contains(ct, "html") && !strings.Contains(ct, "text") {
		return nil, "", ErrURLNotHTML
	}
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if rerr != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrURLUnreachable, rerr.Error())
	}
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(raw)))
	if derr != nil {
		return nil, "", fmt.Errorf("%w: HTML 解析失败", ErrURLNotHTML)
	}
	title = strings.TrimSpace(doc.Find("title").First().Text())
	doc.Find("script,style,nav,header,footer,aside,noscript").Each(func(_ int, s *goquery.Selection) { _ = s.Remove() })
	text := strings.TrimSpace(doc.Find("body").Text())
	if text == "" {
		return nil, title, ErrURLNotHTML
	}
	body = []byte(text)
	return body, title, nil
}

// ContentKind 按扩展名与内容判定切块策略。
func ContentKind(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".md", ".markdown":
		return "markdown"
	case ".html", ".htm":
		return "text"
	}
	if ext == "" && looksLikeCode(p) {
		return "code"
	}
	if IsCodeExt(ext) {
		return "code"
	}
	return "text"
}

// IsCodeExt 代码扩展名（与 IsSupportedExt 区分：md/txt/yaml 等非代码走文本策略）。
func IsCodeExt(ext string) bool {
	switch ext {
	case ".go", ".py", ".java", ".js", ".ts", ".tsx", ".jsx", ".c", ".h", ".cpp", ".hpp",
		".rs", ".rb", ".php", ".cs", ".swift", ".kt", ".sql", ".sh", ".bash",
		".proto", ".graphql":
		return true
	}
	return false
}

func looksLikeCode(p string) bool {
	name := strings.ToLower(filepath.Base(p))
	return name == "dockerfile" || name == "makefile" || strings.HasSuffix(name, ".dockerfile") || strings.HasSuffix(name, ".makefile")
}

// DetectLanguage go-enry 语言识别（文件清单展示用；无法识别返回空）。
func DetectLanguage(p string) string {
	lang := enry.GetLanguage(p, nil)
	return lang
}
