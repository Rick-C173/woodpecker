package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
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

// streamClient 流式调用专用客户端：**不设** http.Client.Timeout。
// http.Client.Timeout 是整个请求（含读流式 body）的墙钟上限——端到端验证发现长回答
// 生成超过 30s 即被掐断（真实缺陷）。流式生命周期由请求 ctx 控制：SSE 客户端断开、
// handler 超时都会取消 ctx，取消经 ctx 传播即可中止底层连接。
func (p *OpenAIProvider) streamClient(cfg ResolvedConfig) *openai.Client {
	cc := openai.DefaultConfig(cfg.APIKey)
	cc.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	cc.HTTPClient = &http.Client{}
	return openai.NewClientWithConfig(cc)
}

// chatParams 从配置 JSON 解析对话参数（temperature/top_p）。
type chatParams struct {
	Temperature *float32 `json:"temperature"`
	TopP        *float32 `json:"top_p"`
}

// embeddingDefaultDims 向量维度兜底值：与 chunks.embedding 的 vector(1024) 对齐
// （数据库设计.md §3.2）。模型条目未配置 params.dimensions 时按此值校验，
// 使维度不符在 Provider 层就报出可读错误，而不是让 DB 抛
// `expected 1024 dimensions, not 1536`（审计 P1-14）。
const embeddingDefaultDims = 1024

// paramInt 从 params JSON 里读取整数字段（keys 按优先级排列；兼容数字与数字字符串）。
// 未配置 / 类型非法 → (0,false)，调用方据此决定「不发送该字段」。
func paramInt(params string, keys ...string) (int, bool) {
	if strings.TrimSpace(params) == "" {
		return 0, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(params), &m); err != nil {
		return 0, false
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			if t != math.Trunc(t) {
				continue // 非整数（如 1024.5）视为非法配置
			}
			return int(t), true
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(t))
			if err != nil {
				continue
			}
			return n, true
		}
	}
	return 0, false
}

// embeddingDims 期望的向量维度：优先 params.dimensions，兼容 params.dims，兜底 1024。
// hasExplicit 表示模型条目是否显式配置了维度（决定是否向服务端发送 dimensions）。
func embeddingDims(params string) (dims int, hasExplicit bool) {
	if n, ok := paramInt(params, "dimensions", "dims"); ok && n > 0 {
		return n, true
	}
	return embeddingDefaultDims, false
}

func classifyErr(err error, action string) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%w（%s）: %s", ErrBadResponse, action, apiErr.Error())
	}
	return fmt.Errorf("%w（%s）: %s", ErrUnavailable, action, err.Error())
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
	stream, err := p.streamClient(cfg).CreateChatCompletionStream(ctx, creq)
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
//
// 维度处理（审计 P1-14）：模型条目 params 里配了 dimensions（或 dims）就随请求发送，
// 并对返回向量做维度校验；未配置则按 vector(1024) 校验。
// 校验失败返回可读错误，避免脏数据一路走到 DB 才报 `expected 1024 dimensions`。
func (p *OpenAIProvider) Embed(ctx context.Context, cfg ResolvedConfig, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	expectDims, hasExplicit := embeddingDims(cfg.Params)
	req := openai.EmbeddingRequest{
		Model: openai.EmbeddingModel(cfg.ModelName),
		Input: texts,
	}
	if hasExplicit {
		req.Dimensions = expectDims // 0 值表示不发送（见 go-openai 的 omitempty）
	}
	resp, err := p.client(cfg).CreateEmbeddings(ctx, req)
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
		if got := len(d.Embedding); got != expectDims {
			return nil, fmt.Errorf(
				"%w（向量化）: 向量化维度不符: 期望 %d 实际 %d（请检查模型条目 params.dimensions 或更换模型）",
				ErrBadResponse, expectDims, got)
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

// rerankTopN 重排请求的 top_n：默认取候选文档数（全量打分），
// 模型条目 params.top_n 存在时透传该值（审计 P1-14 同一 params 契约）。
func rerankTopN(params string, docs int) int {
	if n, ok := paramInt(params, "top_n"); ok && n > 0 {
		return n
	}
	return docs
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
	payload, _ := json.Marshal(rerankRequest{
		Model: cfg.ModelName, Query: query, Documents: docs, TopN: rerankTopN(cfg.Params, len(docs)),
	})
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
		return nil, fmt.Errorf("%w（重排）: %s", ErrBadResponse, err.Error())
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
