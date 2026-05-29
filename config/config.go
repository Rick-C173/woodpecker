package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 应用全局配置
type Config struct {
	Server ServerConfig `yaml:"server"`
	LLM    LLMConfig    `yaml:"llm"`
	Review ReviewConfig `yaml:"review"`
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
}

// Addr 返回服务监听地址
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}
