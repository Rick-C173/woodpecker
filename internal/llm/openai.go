package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// 分类错误：网络层不可达 → ErrUnavailable；API 返回异常 → ErrBadResponse。
var (
	ErrUnavailable = errors.New("模型服务不可用")
	ErrBadResponse = errors.New("模型服务无有效响应")
)

// OpenAIProvider OpenAI 兼容 API 实现（无状态：配置随调用注入）。
type OpenAIProvider struct {
	HTTPClient *http.Client
	Timeout    time.Duration // 非流式调用超时
}

func NewOpenAIProvider() *OpenAIProvider {
	return &OpenAIProvider{Timeout: 30 * time.Second}
}

func (p *OpenAIProvider) client(cfg ResolvedConfig) *openai.Client {
	cc := openai.DefaultConfig(cfg.APIKey)
	cc.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	httpClient := &http.Client{}
	if p.Timeout > 0 {
		httpClient.Timeout = p.Timeout
	}
	cc.HTTPClient = httpClient
	return openai.NewClientWithConfig(cc)
}

// chatParams 从配置 JSON 解析对话参数（temperature/top_p）。
type chatParams struct {
	Temperature *float32 `json:"temperature"`
	TopP        *float32 `json:"top_p"`
}

func classifyErr(err error, action string) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%w（%s）: %s", ErrBadResponse, action, apiErr.Error())
	}
	return fmt.Errorf("%w（%s）: %v", ErrUnavailable, action, err)
}

// ChatStream 流式对话（Provider 接口实现）。
func (p *OpenAIProvider) ChatStream(ctx context.Context, cfg ResolvedConfig, req ChatRequest) (<-chan ChatDelta, error) {
	params := chatParams{}
	_ = json.Unmarshal([]byte(cfg.Params), &params)

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: req.System},
		{Role: openai.ChatMessageRoleUser, Content: req.Prompt},
	}
	creq := openai.ChatCompletionRequest{
		Model:    cfg.ModelName,
		Messages: messages,
		Stream:   true,
	}
	if params.Temperature != nil {
		creq.Temperature = *params.Temperature
	}
	if params.TopP != nil {
		creq.TopP = *params.TopP
	}
	stream, err := p.client(cfg).CreateChatCompletionStream(ctx, creq)
	if err != nil {
		return nil, classifyErr(err, "对话")
	}
	ch := make(chan ChatDelta, 16)
	go func() {
		defer close(ch)
		defer func() { _ = stream.Close() }()
		for {
			resp, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					// 上游异常：把错误注入通道后结束（消费方感知内容中断）
					ch <- ChatDelta{Content: "\n[生成中断] " + classifyErr(err, "对话").Error()}
				}
				return
			}
			if len(resp.Choices) == 0 {
				continue
			}
			delta := resp.Choices[0].Delta.Content
			if delta == "" {
				continue
			}
			select {
			case ch <- ChatDelta{Content: delta}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// Embed 批量向量化（Provider 接口实现）。
func (p *OpenAIProvider) Embed(ctx context.Context, cfg ResolvedConfig, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	resp, err := p.client(cfg).CreateEmbeddings(ctx, openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(cfg.ModelName),
		Input: texts,
	})
	if err != nil {
		return nil, classifyErr(err, "向量化")
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("%w（向量化）: 返回 %d 条，期望 %d 条", ErrBadResponse, len(resp.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("%w（向量化）: 非法索引 %d", ErrBadResponse, d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

// rerankRequest / rerankResponse SiliconFlow/Jina 风格 /v1/rerank。
type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n"`
}

type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

// Rerank 按相关度重排（Provider 接口实现）。
func (p *OpenAIProvider) Rerank(ctx context.Context, cfg ResolvedConfig, query string, docs []string) ([]float64, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	payload, _ := json.Marshal(rerankRequest{Model: cfg.ModelName, Query: query, Documents: docs, TopN: len(docs)})
	url := strings.TrimSuffix(cfg.BaseURL, "/") + "/rerank"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, classifyErr(err, "重排")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: p.Timeout}
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, classifyErr(err, "重排")
	}
	defer func() { _ = httpResp.Body.Close() }()
	if httpResp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(httpResp.Body, 300))
		return nil, fmt.Errorf("%w（重排）: HTTP %d %s", ErrBadResponse, httpResp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var rr rerankResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&rr); err != nil {
		return nil, fmt.Errorf("%w（重排）: %v", ErrBadResponse, err)
	}
	scores := make([]float64, len(docs))
	for _, r := range rr.Results {
		if r.Index >= 0 && r.Index < len(docs) {
			scores[r.Index] = r.RelevanceScore
		}
	}
	return scores, nil
}

// ---- 连通性测试（Tester 实现）----

// TestChat 对话连通性：发起一次 max_tokens=1 的补全。
func (p *OpenAIProvider) TestChat(ctx context.Context, cfg ResolvedConfig) error {
	_, err := p.client(cfg).CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:               cfg.ModelName,
		Messages:            []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "ping"}},
		MaxCompletionTokens: 1,
	})
	return classifyErr(err, "对话测试")
}

// TestEmbedding 向量化连通性。
func (p *OpenAIProvider) TestEmbedding(ctx context.Context, cfg ResolvedConfig) error {
	_, err := p.Embed(ctx, cfg, []string{"ping"})
	return err
}

// TestRerank 重排连通性。
func (p *OpenAIProvider) TestRerank(ctx context.Context, cfg ResolvedConfig) error {
	_, err := p.Rerank(ctx, cfg, "ping", []string{"pong"})
	return err
}
