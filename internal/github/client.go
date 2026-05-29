package github

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client GitHub API 客户端，用于操作 PR
type Client struct {
	token      string
	httpClient *http.Client
	apiURL     string
}

// NewClient 创建 GitHub API 客户端
// token: GitHub Personal Access Token 或 GitHub App Installation Token
// apiURL: 默认 https://api.github.com，企业版自定义
func NewClient(token, apiURL string) *Client {
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	return &Client{
		token:  token,
		apiURL: strings.TrimRight(apiURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// PRInfo Pull Request 基本信息
type PRInfo struct {
	Owner    string
	Repo     string
	Number   int
	BaseRef  string // 目标分支（如 main）
	HeadRef  string // 源分支
	BaseSHA  string // 目标分支最新 commit
	HeadSHA  string // 源分支最新 commit
	Title    string
	CloneURL string // 仓库克隆地址
}

// ReviewComment PR 上的审查评论
type ReviewComment struct {
	Body     string `json:"body"`
	Path     string `json:"path,omitempty"`     // 文件路径
	Line     int    `json:"line,omitempty"`     // 行号
	Side     string `json:"side,omitempty"`     // RIGHT / LEFT
	Position int    `json:"position,omitempty"` // diff 中的位置（已弃用，新API用line）
}

// PRReview PR 审查，可包含多条评论
type PRReview struct {
	Body     string          `json:"body"`
	Event    string          `json:"event"` // APPROVE / REQUEST_CHANGES / COMMENT
	Comments []ReviewComment `json:"comments,omitempty"`
}

// CreateReview 创建 PR 审查（带评论）
func (c *Client) CreateReview(owner, repo string, prNumber int, review PRReview) error {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", c.apiURL, owner, repo, prNumber)

	body, err := json.Marshal(review)
	if err != nil {
		return fmt.Errorf("marshal review: %w", err)
	}

	_, err = c.doRequest(http.MethodPost, url, body)
	return err
}

// CreateReviewComment 创建单条 PR 审查评论（在特定代码行上）
func (c *Client) CreateReviewComment(owner, repo string, prNumber int, commitID, path, body string, line int) error {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments", c.apiURL, owner, repo, prNumber)

	req := map[string]interface{}{
		"body":      body,
		"commit_id": commitID,
		"path":      path,
		"line":      line,
		"side":      "RIGHT",
	}

	jsonBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal comment: %w", err)
	}

	_, err = c.doRequest(http.MethodPost, url, jsonBody)
	return err
}

// UpdatePRStatus 更新 PR 状态（pending/success/failure）
func (c *Client) UpdatePRStatus(owner, repo, commitSHA, state, description, context string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/statuses/%s", c.apiURL, owner, repo, commitSHA)

	req := map[string]string{
		"state":       state,       // pending / success / failure / error
		"description": description, // 状态描述
		"context":     context,     // 上下文标识（如 "woodpecker/review"）
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	_, err = c.doRequest(http.MethodPost, url, body)
	return err
}

// GetPR 获取 PR 详细信息
func (c *Client) GetPR(owner, repo string, prNumber int) (*PRInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.apiURL, owner, repo, prNumber)

	respBody, err := c.doRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	var pr struct {
		Number int `json:"number"`
		Base   struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"base"`
		Head struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"head"`
		Title string `json:"title"`
	}

	if err := json.Unmarshal(respBody, &pr); err != nil {
		return nil, fmt.Errorf("parse PR response: %w", err)
	}

	// extract owner/repo from PR response
	if owner == "" {
		parts := strings.Split(pr.Base.Repo.CloneURL, "/")
		if len(parts) >= 2 {
			owner = parts[len(parts)-2]
		}
	}
	if repo == "" {
		parts := strings.Split(pr.Base.Repo.CloneURL, "/")
		if len(parts) >= 1 {
			repo = strings.TrimSuffix(parts[len(parts)-1], ".git")
		}
	}

	return &PRInfo{
		Owner:    owner,
		Repo:     repo,
		Number:   pr.Number,
		BaseRef:  pr.Base.Ref,
		HeadRef:  pr.Head.Ref,
		BaseSHA:  pr.Base.SHA,
		HeadSHA:  pr.Head.SHA,
		Title:    pr.Title,
		CloneURL: pr.Head.Repo.CloneURL,
	}, nil
}

// doRequest 发送 HTTP 请求
func (c *Client) doRequest(method, url string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// VerifySignature 验证 GitHub Webhook HMAC 签名
func VerifySignature(secret string, payload []byte, signatureHeader string) bool {
	if secret == "" || signatureHeader == "" {
		return false
	}

	// GitHub 签名格式: sha256=xxx
	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return false
	}

	expectedMAC := strings.TrimPrefix(signatureHeader, prefix)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	actualMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedMAC), []byte(actualMAC))
}
