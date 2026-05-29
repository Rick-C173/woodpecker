package main

import (
	"fmt"
	"log"
	"os"

	"woodpecker/config"
	"woodpecker/engine/diff"
	"woodpecker/engine/llm"
	"woodpecker/service"
)

func main() {
	fmt.Println("=== Woodpecker 阶段一功能验证 ===")

	// 1. 测试 diff 解析
	fmt.Println("\n1. 测试 diff 解析...")
	diffText, err := os.ReadFile("test/sample.diff")
	if err != nil {
		log.Fatalf("读取 diff 文件失败: %v", err)
	}

	fileDiffs, err := diff.Parse(string(diffText))
	if err != nil {
		log.Fatalf("解析 diff 失败: %v", err)
	}
	fmt.Printf("✓ 解析成功，共 %d 个文件\n", len(fileDiffs))
	for i, fd := range fileDiffs {
		fmt.Printf("  文件 %d: %s → %s (%s, %d 个 hunk)\n",
			i+1, fd.OldPath, fd.NewPath, fd.Status, len(fd.Hunks))
	}

	// 2. 测试 LLM 客户端（Mock）
	fmt.Println("\n2. 测试 LLM 客户端（Mock）...")
	mockClient := llm.NewMockClient()
	llmReq := llm.ReviewRequest{
		FileDiffs: fileDiffs,
		Language:  "go",
	}
	llmResp, err := mockClient.Review(nil, llmReq)
	if err != nil {
		log.Fatalf("Mock LLM 调用失败: %v", err)
	}
	fmt.Printf("✓ Mock LLM 调用成功，返回 %d 条评论\n", len(llmResp.Comments))
	fmt.Printf("  总结: %s\n", llmResp.Summary)

	// 3. 测试服务层
	fmt.Println("\n3. 测试服务层...")
	reviewer := service.NewReviewer(mockClient, 50000, "go")
	svcReq := service.ReviewRequest{
		DiffText: string(diffText),
		Language: "go",
	}
	svcResp := reviewer.Review(nil, svcReq)
	if svcResp.Error != "" {
		log.Fatalf("服务层审查失败: %v", svcResp.Error)
	}
	fmt.Printf("✓ 服务层审查成功，耗时 %s\n", svcResp.Elapsed)
	fmt.Printf("  发现 %d 个问题\n", svcResp.Result.Stats.TotalIssues)
	fmt.Printf("  严重等级分布: %+v\n", svcResp.Result.Stats.BySeverity)

	// 4. 测试配置加载
	fmt.Println("\n4. 测试配置加载...")
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	fmt.Printf("✓ 配置加载成功\n")
	fmt.Printf("  服务地址: %s\n", cfg.Server.Addr())
	fmt.Printf("  LLM 提供商: %s\n", cfg.LLM.Provider)
	fmt.Printf("  审查语言: %s\n", cfg.Review.DefaultLanguage)

	fmt.Println("\n=== 阶段一验证完成 ===")
	fmt.Println("所有核心模块（diff 解析、LLM 客户端、服务层、配置）均正常工作。")
	fmt.Println("下一步：启动 HTTP 服务并测试 API 端点。")
}
