package rag

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
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

// gitTokenUsername 私有仓库 token 认证的用户名位取值。
// GitLab PAT 要求 `oauth2:<token>`；GitHub / Bitbucket 同样接受该组合，
// 故统一用它覆盖三类平台的 HTTPS 认证（审计 P2-36）。
const gitTokenUsername = "oauth2"

// FetchGit 克隆仓库（浅克隆）并读取受支持文件。
// credential 为空时匿名拉取；非空时按 access token 处理（HTTPS BasicAuth，token 放密码位）。
func FetchGit(ctx context.Context, repoURL, gitRef, credential string, maxRepoBytes int64, maxFiles int, ignoreRules []string, excl ExclusionRules) (*FetchResult, error) {
	tmp, err := os.MkdirTemp("", "wp-clone-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cloneOpts := &git.CloneOptions{URL: repoURL, Depth: 1, SingleBranch: true}
	if gitRef != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(gitRef)
	}
	if credential != "" {
		// 私有仓库 token 必须放**密码位**：GitLab PAT 只认 `oauth2:<token>`，
		// GitHub / Bitbucket 也接受该形式；反之把 token 放用户名位会让 GitLab 必 401（审计 P2-36）。
		cloneOpts.Auth = &githttp.BasicAuth{Username: gitTokenUsername, Password: credential}
	}
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
