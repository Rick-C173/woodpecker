package vector

import (
	"context"
	"testing"

	"woodpecker/config"
)

func TestNewEmbedder(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantErr  bool
	}{
		{
			name:     "openai provider",
			provider: "openai",
			wantErr:  false,
		},
		{
			name:     "ollama provider",
			provider: "ollama",
			wantErr:  false,
		},
		{
			name:     "unknown provider defaults to openai",
			provider: "unknown",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.EmbeddingConfig{
				Provider:  tt.provider,
				Dimension: 1536,
			}

			embedder, err := NewEmbedder(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewEmbedder() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if embedder != nil && embedder.GetDimension() != 1536 {
				t.Errorf("NewEmbedder() dimension = %d, want 1536", embedder.GetDimension())
			}
		})
	}
}

func TestOpenAIEmbedder_GetDimension(t *testing.T) {
	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		Dimension: 1536,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder failed: %v", err)
	}

	if embedder.GetDimension() != 1536 {
		t.Errorf("GetDimension() = %d, want 1536", embedder.GetDimension())
	}
}

func TestOllamaEmbedder_GetDimension(t *testing.T) {
	embedder, err := NewOllamaEmbedder(config.EmbeddingConfig{
		Dimension: 768,
	})
	if err != nil {
		t.Fatalf("NewOllamaEmbedder failed: %v", err)
	}

	if embedder.GetDimension() != 768 {
		t.Errorf("GetDimension() = %d, want 768", embedder.GetDimension())
	}
}

func TestOpenAIEmbedder_EmbedOne(t *testing.T) {
	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		APIKey:    "",
		Model:     "text-embedding-3-small",
		Dimension: 1536,
		Timeout:   10,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder failed: %v", err)
	}

	ctx := context.Background()

	vector, err := embedder.EmbedOne(ctx, "Hello, world!")
	if err != nil {
		t.Logf("EmbedOne failed (expected without API key): %v", err)
		return
	}

	if len(vector) != 1536 {
		t.Errorf("EmbedOne returned vector of length %d, want 1536", len(vector))
	}
}

func TestOpenAIEmbedder_Embed(t *testing.T) {
	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		APIKey:    "",
		Model:     "text-embedding-3-small",
		Dimension: 1536,
		Timeout:   10,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder failed: %v", err)
	}

	ctx := context.Background()

	vectors, err := embedder.Embed(ctx, []string{"Hello", "World"})
	if err != nil {
		t.Logf("Embed failed (expected without API key): %v", err)
		return
	}

	if len(vectors) != 2 {
		t.Errorf("Embed returned %d vectors, want 2", len(vectors))
	}

	for i, v := range vectors {
		if len(v) != 1536 {
			t.Errorf("Vector %d has length %d, want 1536", i, len(v))
		}
	}
}

func TestOpenAIEmbedder_Embed_Empty(t *testing.T) {
	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		Dimension: 1536,
	})
	if err != nil {
		t.Fatalf("NewOpenAIEmbedder failed: %v", err)
	}

	ctx := context.Background()

	vectors, err := embedder.Embed(ctx, []string{})
	if err != nil {
		t.Errorf("Embed with empty input should not error, got: %v", err)
	}

	if vectors != nil {
		t.Errorf("Embed with empty input should return nil, got: %v", vectors)
	}
}
