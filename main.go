package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"

	"woodpecker/config"
	"woodpecker/internal/engine/llm"
	"woodpecker/internal/git"
	"woodpecker/internal/github"
	"woodpecker/internal/handler"
	"woodpecker/internal/pipeline"
	"woodpecker/internal/service"
)

func main() {
	// 1. 加载配置
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 2. 设置 Gin 模式
	gin.SetMode(cfg.Server.Mode)

	// 3. 初始化 LLM 客户端
	var llmClient llm.LlmClient
	if cfg.LLM.APIKey == "" {
		log.Println("警告: API_KEY 未配置，使用 Mock 客户端")
		llmClient = llm.NewMockClient()
	} else {
		llmClient = llm.NewOpenAIClient(cfg.LLM)
		log.Printf("使用 %s 模型 (%s)", cfg.LLM.Provider, cfg.LLM.Model)
	}

	// 4. 创建服务层
	reviewer := service.NewReviewer(
		llmClient,
		cfg.Review.MaxDiffChars,
		cfg.Review.DefaultLanguage,
	)

	// 5. 创建处理器
	reviewHandler := handler.NewReviewHandler(reviewer)

	// 6. 初始化 GitHub 集成（可选）
	var prProcessor *pipeline.PRProcessor
	if cfg.GitHub.Token != "" {
		githubClient := github.NewClient(cfg.GitHub.Token, cfg.GitHub.APIURL)
		gitExecutor := git.NewExecutor(cfg.GitHub.WorkDir)
		prProcessor = pipeline.NewPRProcessor(
			gitExecutor,
			githubClient,
			reviewer,
			cfg.GitHub.WorkDir,
		)
		log.Println("GitHub 集成已启用")
	}

	webhookHandler := github.NewWebhookHandler(cfg.GitHub.WebhookSecret)
	webhookCtrl := handler.NewWebhookController(webhookHandler, prProcessor)

	// 7. 设置路由
	router := gin.Default()
	router.GET("/health", reviewHandler.Health)
	router.POST("/api/v1/review", reviewHandler.Review)
	router.POST("/webhook", webhookCtrl.Handle)

	// 8. 启动服务
	addr := cfg.Server.Addr()
	log.Printf("服务启动中，监听地址: %s", addr)

	go func() {
		if err := router.Run(addr); err != nil {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 9. 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")
}
