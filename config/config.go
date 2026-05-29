package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用全局配置
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	LLM       LLMConfig       `yaml:"llm"`
	GitHub    GitHubConfig    `yaml:"github"`
	Review    ReviewConfig    `yaml:"review"`
	Database  DatabaseConfig  `yaml:"database"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	Vector    VectorConfig    `yaml:"vector"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host     string `yaml:"host"`      // 数据库地址
	Port     int    `yaml:"port"`      // 数据库端口
	User     string `yaml:"user"`      // 用户名
	Password string `yaml:"password"`  // 密码
	Database string `yaml:"database"`  // 数据库名
	SSLMode  string `yaml:"ssl_mode"`  // SSL 模式
	MaxConns int    `yaml:"max_conns"` // 最大连接数
}

// EmbeddingConfig Embedding API 配置
type EmbeddingConfig struct {
	Provider  string `yaml:"provider"`   // openai / ollama（本地模型）
	APIKey    string `yaml:"api_key"`    // API 密钥
	Model     string `yaml:"model"`      // 模型名称：text-embedding-3-small / nomic-embed-text
	BaseURL   string `yaml:"base_url"`   // 自定义 API 端点
	Dimension int    `yaml:"dimension"`  // 向量维度，默认 1536（OpenAI）
	Timeout   int    `yaml:"timeout"`    // 请求超时（秒）
	BatchSize int    `yaml:"batch_size"` // 批量大小
}

// VectorConfig 向量库配置
type VectorConfig struct {
	Enabled        bool    `yaml:"enabled"`         // 是否启用向量功能
	CollectionName string  `yaml:"collection_name"` // Collection 名称
	MaxResults     int     `yaml:"max_results"`     // 最大返回结果数
	ScoreThreshold float64 `yaml:"score_threshold"` // 相似度阈值
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Host string `yaml:"host"` // 监听地址，默认 0.0.0.0
	Port int    `yaml:"port"` // 监听端口，默认 8080
	Mode string `yaml:"mode"` // gin 模式：debug / release / test
}

// LLMConfig LLM 提供商配置
type LLMConfig struct {
	Provider  string `yaml:"provider"`   // openai / claude / ollama / deepseek
	APIKey    string `yaml:"api_key"`    // API 密钥
	Model     string `yaml:"model"`      // 模型名称
	BaseURL   string `yaml:"base_url"`   // 自定义 API 端点
	MaxTokens int    `yaml:"max_tokens"` // 单次请求最大 token 数
	Timeout   int    `yaml:"timeout"`    // 请求超时（秒），默认 60
}

// GitHubConfig GitHub 集成配置
type GitHubConfig struct {
	Token         string `yaml:"token"`          // GitHub Personal Access Token
	WebhookSecret string `yaml:"webhook_secret"` // Webhook 签名密钥
	APIURL        string `yaml:"api_url"`        // GitHub API 地址（企业版自定义）
	WorkDir       string `yaml:"work_dir"`       // 本地仓库缓存目录
}

// ReviewConfig 审查行为配置
type ReviewConfig struct {
	MaxFilesPerRequest int      `yaml:"max_files_per_request"` // 单次审查最大文件数
	MaxDiffChars       int      `yaml:"max_diff_chars"`        // 单次审查最大 diff 字符数
	IgnorePatterns     []string `yaml:"ignore_patterns"`       // 忽略的文件模式（glob）
	DefaultLanguage    string   `yaml:"default_language"`      // 默认编程语言
}

// Default 返回默认配置
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
			Mode: "debug",
		},
		LLM: LLMConfig{
			Provider:  "openai",
			Model:     "gpt-4o-mini",
			MaxTokens: 4096,
			Timeout:   60,
		},
		GitHub: GitHubConfig{
			APIURL:  "https://api.github.com",
			WorkDir: "./repos",
		},
		Review: ReviewConfig{
			MaxFilesPerRequest: 20,
			MaxDiffChars:       50000,
			DefaultLanguage:    "go",
			IgnorePatterns: []string{
				"*.lock",
				"*.sum",
				"vendor/**",
				"node_modules/**",
				"*.pb.go",
				"*_gen.go",
			},
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Password: "",
			Database: "woodpecker",
			SSLMode:  "disable",
			MaxConns: 10,
		},
		Embedding: EmbeddingConfig{
			Provider:  "openai",
			Model:     "text-embedding-3-small",
			Dimension: 1536,
			Timeout:   30,
			BatchSize: 100,
		},
		Vector: VectorConfig{
			Enabled:        false,
			CollectionName: "code_chunks",
			MaxResults:     5,
			ScoreThreshold: 0.7,
		},
	}
}

// Load 从 YAML 文件加载配置，文件不存在时返回默认配置
func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}

	// 环境变量覆盖
	applyEnvOverrides(cfg)

	return cfg, nil
}

// applyEnvOverrides 用环境变量覆盖配置
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("WOODPECKER_LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("WOODPECKER_LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}
	if v := os.Getenv("WOODPECKER_LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("WOODPECKER_LLM_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("WOODPECKER_SERVER_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Server.Port)
	}
	if v := os.Getenv("WOODPECKER_DB_HOST"); v != "" {
		cfg.Database.Host = v
	}
	if v := os.Getenv("WOODPECKER_DB_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Database.Port)
	}
	if v := os.Getenv("WOODPECKER_DB_USER"); v != "" {
		cfg.Database.User = v
	}
	if v := os.Getenv("WOODPECKER_DB_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("WOODPECKER_DB_NAME"); v != "" {
		cfg.Database.Database = v
	}
	if v := os.Getenv("WOODPECKER_EMBEDDING_API_KEY"); v != "" {
		cfg.Embedding.APIKey = v
	}
	if v := os.Getenv("WOODPECKER_EMBEDDING_PROVIDER"); v != "" {
		cfg.Embedding.Provider = v
	}
	if v := os.Getenv("WOODPECKER_EMBEDDING_MODEL"); v != "" {
		cfg.Embedding.Model = v
	}
	if v := os.Getenv("WOODPECKER_EMBEDDING_BASE_URL"); v != "" {
		cfg.Embedding.BaseURL = v
	}
	if v := os.Getenv("WOODPECKER_VECTOR_ENABLED"); v != "" {
		cfg.Vector.Enabled = v == "true" || v == "1"
	}
}

// Addr 返回服务监听地址
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// DSN 返回 PostgreSQL 连接字符串
func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.Database, d.SSLMode)
}
