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
	"woodpecker/internal/store"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	gin.SetMode(cfg.Server.Mode)

	var db *store.Store
	if cfg.Database.Host != "" && cfg.Database.Database != "" {
		db, err = store.NewStore(cfg.Database)
		if err != nil {
			log.Printf("警告: 数据库连接失败: %v，将使用无持久化模式", err)
		} else {
			log.Printf("数据库连接成功: %s:%d/%s", cfg.Database.Host, cfg.Database.Port, cfg.Database.Database)
			defer db.Close()
		}
	} else {
		log.Println("警告: 数据库未配置，将使用无持久化模式")
	}

	var llmClient llm.LlmClient
	if cfg.LLM.APIKey == "" {
		log.Println("警告: API_KEY 未配置，使用 Mock 客户端")
		llmClient = llm.NewMockClient()
	} else {
		llmClient = llm.NewOpenAIClient(cfg.LLM)
		log.Printf("使用 %s 模型 (%s)", cfg.LLM.Provider, cfg.LLM.Model)
	}

	var reviewer *service.Reviewer
	if db != nil {
		reviewer = service.NewReviewerWithStore(
			llmClient,
			cfg.Review.MaxDiffChars,
			cfg.Review.DefaultLanguage,
			db,
		)
	} else {
		reviewer = service.NewReviewer(
			llmClient,
			cfg.Review.MaxDiffChars,
			cfg.Review.DefaultLanguage,
		)
	}

	reviewHandler := handler.NewReviewHandler(reviewer, db)
	webhookHandler := github.NewWebhookHandler(cfg.GitHub.WebhookSecret)

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

	webhookCtrl := handler.NewWebhookController(webhookHandler, prProcessor, db)

	router := gin.Default()
	router.GET("/health", reviewHandler.Health)
	router.POST("/api/v1/review", reviewHandler.Review)
	router.POST("/webhook", webhookCtrl.Handle)

	if db != nil {
		apiHandler := handler.NewAPIHandler(db)
		router.GET("/api/v1/projects", apiHandler.ListProjects)
		router.GET("/api/v1/projects/:owner/:repo/reviews", apiHandler.ListReviews)
		router.GET("/api/v1/reviews/:id", apiHandler.GetReview)
		router.GET("/api/v1/stats", apiHandler.GetStats)
		log.Println("历史审查 API 已启用")
	}

	addr := cfg.Server.Addr()
	log.Printf("服务启动中，监听地址: %s", addr)

	go func() {
		if err := router.Run(addr); err != nil {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")
}
