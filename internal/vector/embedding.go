package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"woodpecker/config"
)

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	EmbedOne(ctx context.Context, text string) ([]float32, error)
	GetDimension() int
}

type OpenAIEmbedder struct {
	client    *http.Client
	baseURL   string
	apiKey    string
	model     string
	dimension int
}

func NewOpenAIEmbedder(cfg config.EmbeddingConfig) (*OpenAIEmbedder, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 30
	}

	dimension := cfg.Dimension
	if dimension == 0 {
		dimension = 1536
	}

	return &OpenAIEmbedder{
		client: &http.Client{
			Timeout: time.Duration(cfg.Timeout) * time.Second,
		},
		baseURL:   cfg.BaseURL,
		apiKey:    cfg.APIKey,
		model:     cfg.Model,
		dimension: dimension,
	}, nil
}

type openAIRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type openAIResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := openAIRequest{
		Input: texts,
		Model: e.model,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API returned status %d", resp.StatusCode)
	}

	var result openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	embeddings := make([][]float32, len(result.Data))
	for i, data := range result.Data {
		if len(data.Embedding) != e.dimension {
			return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d", e.dimension, len(data.Embedding))
		}
		embeddings[i] = data.Embedding
	}

	return embeddings, nil
}

func (e *OpenAIEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return embeddings[0], nil
}

func (e *OpenAIEmbedder) GetDimension() int {
	return e.dimension
}

type OllamaEmbedder struct {
	client    *http.Client
	baseURL   string
	model     string
	dimension int
}

func NewOllamaEmbedder(cfg config.EmbeddingConfig) (*OllamaEmbedder, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 60
	}

	dimension := cfg.Dimension
	if dimension == 0 {
		dimension = 768
	}

	return &OllamaEmbedder{
		client: &http.Client{
			Timeout: time.Duration(cfg.Timeout) * time.Second,
		},
		baseURL:   cfg.BaseURL,
		model:     cfg.Model,
		dimension: dimension,
	}, nil
}

type ollamaRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type ollamaResponse struct {
	Embedding []float32 `json:"embedding"`
}

func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	embeddings := make([][]float32, 0, len(texts))

	for _, text := range texts {
		reqBody := ollamaRequest{
			Input: text,
			Model: e.model,
		}

		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embeddings", bytes.NewBuffer(jsonData))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		resp, err := e.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		var result ollamaResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode response: %w", err)
		}
		resp.Body.Close()

		if len(result.Embedding) != e.dimension {
			return nil, fmt.Errorf("embedding dimension mismatch: expected %d, got %d", e.dimension, len(result.Embedding))
		}

		embeddings = append(embeddings, result.Embedding)
	}

	return embeddings, nil
}

func (e *OllamaEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return embeddings[0], nil
}

func (e *OllamaEmbedder) GetDimension() int {
	return e.dimension
}

func NewEmbedder(cfg config.EmbeddingConfig) (Embedder, error) {
	switch cfg.Provider {
	case "ollama":
		return NewOllamaEmbedder(cfg)
	case "openai":
		fallthrough
	default:
		return NewOpenAIEmbedder(cfg)
	}
}
