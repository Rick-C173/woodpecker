package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"woodpecker/config"
)

// OpenAIClient 基于 OpenAI 兼容 API 的 LLM 客户端
// 支持 OpenAI / DeepSeek / Ollama / 任何兼容接口
type OpenAIClient struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIClient 创建 OpenAI 兼容客户端
func NewOpenAIClient(cfg config.LLMConfig) *OpenAIClient {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	return &OpenAIClient{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.Timeout) * time.Second,
		},
	}
}

// chatRequest OpenAI Chat Completions 请求体
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse OpenAI Chat Completions 响应体
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Review 调用 LLM 进行代码审查
func (c *OpenAIClient) Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error) {
	// 构建 prompt
	builder, err := NewPromptBuilder()
	if err != nil {
		return nil, fmt.Errorf("create prompt builder: %w", err)
	}

	prompt, err := builder.Build(req)
	if err != nil {
		return nil, fmt.Errorf("build prompt: %w", err)
	}

	// 构建 API 请求
	chatReq := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1, // 低温度以获得更一致的审查结果
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 发送 HTTP 请求
	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	// 解析响应
	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse API response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("LLM API error: %s (%s)", chatResp.Error.Message, chatResp.Error.Type)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned empty choices")
	}

	rawOutput := chatResp.Choices[0].Message.Content

	// 解析 LLM 输出为结构化结果
	reviewResp, err := ParseReviewResponse(rawOutput)
	if err != nil {
		// 解析失败时返回原始输出，方便调试
		return &ReviewResponse{
			Comments:  nil,
			Summary:   "LLM 返回格式异常，请检查原始输出",
			RawOutput: rawOutput,
		}, nil
	}

	return reviewResp, nil
}
