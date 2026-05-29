package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WebhookEvent GitHub Webhook 事件类型
type WebhookEvent struct {
	Action      string `json:"action"` // opened / synchronize / reopened / closed
	PullRequest struct {
		Number int `json:"number"`
		Base   struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Title string `json:"title"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// WebhookHandler Webhook 事件处理器
type WebhookHandler struct {
	secret string
}

// NewWebhookHandler 创建 Webhook 处理器
func NewWebhookHandler(secret string) *WebhookHandler {
	return &WebhookHandler{secret: secret}
}

// ParseEvent 解析 Webhook 请求体为结构化事件
func (h *WebhookHandler) ParseEvent(r *http.Request) (*WebhookEvent, error) {
	// 验证签名
	if h.secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}

		if !VerifySignature(h.secret, body, sig) {
			return nil, fmt.Errorf("invalid webhook signature")
		}
	}

	// 解析事件
	var event WebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		return nil, fmt.Errorf("parse webhook payload: %w", err)
	}

	return &event, nil
}

// ShouldReview 判断是否需要触发代码审查
// 仅在 PR 打开、重新打开、或推送新代码时触发
func (h *WebhookHandler) ShouldReview(event *WebhookEvent) bool {
	if event == nil {
		return false
	}

	switch event.Action {
	case "opened", "reopened", "synchronize":
		return true
	default:
		return false
	}
}

// ExtractPRInfo 从 Webhook 事件中提取 PR 信息
func ExtractPRInfo(event *WebhookEvent) *PRInfo {
	if event == nil {
		return nil
	}

	// 从 full_name 解析 owner/repo
	parts := strings.SplitN(event.Repository.FullName, "/", 2)
	owner, repo := "", ""
	if len(parts) == 2 {
		owner = parts[0]
		repo = parts[1]
	}

	return &PRInfo{
		Owner:    owner,
		Repo:     repo,
		Number:   event.PullRequest.Number,
		BaseRef:  event.PullRequest.Base.Ref,
		HeadRef:  event.PullRequest.Head.Ref,
		BaseSHA:  event.PullRequest.Base.SHA,
		HeadSHA:  event.PullRequest.Head.SHA,
		Title:    event.PullRequest.Title,
		CloneURL: event.Repository.CloneURL,
	}
}
