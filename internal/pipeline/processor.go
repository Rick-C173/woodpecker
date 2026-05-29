package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"woodpecker/internal/engine/diff"
	"woodpecker/internal/git"
	"woodpecker/internal/github"
	"woodpecker/internal/model"
	"woodpecker/internal/service"
)

// PRProcessor PR 审查流水线，串联 Git 操作 → Diff 解析 → LLM 审查 → GitHub 评论
type PRProcessor struct {
	gitClient    *git.Executor
	githubClient *github.Client
	reviewer     *service.Reviewer
	workDir      string // 本地仓库工作目录
}

// NewPRProcessor 创建 PR 处理器
func NewPRProcessor(
	gitClient *git.Executor,
	githubClient *github.Client,
	reviewer *service.Reviewer,
	workDir string,
) *PRProcessor {
	return &PRProcessor{
		gitClient:    gitClient,
		githubClient: githubClient,
		reviewer:     reviewer,
		workDir:      workDir,
	}
}

// Process 处理一个 PR 的完整审查流程
func (p *PRProcessor) Process(ctx context.Context, pr *github.PRInfo) error {
	log.Printf("[PR #%d] 开始审查: %s (%s ← %s)", pr.Number, pr.Title, pr.BaseRef, pr.HeadRef)

	// 1. 更新 PR 状态为 pending
	if err := p.githubClient.UpdatePRStatus(pr.Owner, pr.Repo, pr.HeadSHA,
		"pending", "Woodpecker 正在审查代码...", "woodpecker/review"); err != nil {
		log.Printf("[PR #%d] 更新状态失败: %v", pr.Number, err)
		// 不中断流程
	}

	// 2. 准备本地仓库
	repoDir, err := p.prepareRepo(ctx, pr)
	if err != nil {
		p.reportFailure(pr, fmt.Sprintf("准备仓库失败: %v", err))
		return fmt.Errorf("prepare repo: %w", err)
	}

	// 3. 获取 diff（使用本地仓库的 git 执行器）
	localGit := git.NewExecutor(repoDir)
	diffText, err := localGit.Diff(ctx, pr.BaseRef, pr.HeadRef)
	if err != nil {
		p.reportFailure(pr, fmt.Sprintf("获取 diff 失败: %v", err))
		return fmt.Errorf("get diff: %w", err)
	}

	if diffText == "" {
		log.Printf("[PR #%d] diff 为空，跳过审查", pr.Number)
		p.githubClient.UpdatePRStatus(pr.Owner, pr.Repo, pr.HeadSHA,
			"success", "无代码变更需要审查", "woodpecker/review")
		return nil
	}

	// 4. 执行审查
	svcReq := service.ReviewRequest{
		DiffText: diffText,
		Language: "go", // TODO: 自动检测语言
	}

	svcResp := p.reviewer.Review(ctx, svcReq)
	if svcResp.Error != "" {
		p.reportFailure(pr, fmt.Sprintf("审查失败: %s", svcResp.Error))
		return fmt.Errorf("review: %s", svcResp.Error)
	}

	log.Printf("[PR #%d] 审查完成，发现 %d 个问题，耗时 %s",
		pr.Number, svcResp.Result.Stats.TotalIssues, svcResp.Elapsed)

	// 5. 提交审查结果到 GitHub
	if err := p.submitReview(ctx, pr, svcResp.Result); err != nil {
		return fmt.Errorf("submit review: %w", err)
	}

	// 6. 更新 PR 状态为 success
	summary := fmt.Sprintf("审查完成：发现 %d 个问题", svcResp.Result.Stats.TotalIssues)
	if svcResp.Result.Stats.TotalIssues == 0 {
		summary = "审查通过，未发现问题"
	}

	p.githubClient.UpdatePRStatus(pr.Owner, pr.Repo, pr.HeadSHA,
		"success", summary, "woodpecker/review")

	log.Printf("[PR #%d] 审查完成", pr.Number)
	return nil
}

// prepareRepo 准备本地仓库（克隆或更新）
func (p *PRProcessor) prepareRepo(ctx context.Context, pr *github.PRInfo) (string, error) {
	repoDir := filepath.Join(p.workDir, fmt.Sprintf("%s_%s", pr.Owner, pr.Repo))

	// 如果目录已存在，执行 fetch + checkout
	if _, err := os.Stat(repoDir); err == nil {
		gitExec := git.NewExecutor(repoDir)
		if err := gitExec.Fetch(ctx); err != nil {
			log.Printf("fetch 失败，重新克隆: %v", err)
			os.RemoveAll(repoDir)
		} else {
			// 检出 head 分支
			if err := gitExec.Checkout(ctx, pr.HeadRef); err != nil {
				// 尝试 fetch 该分支
				gitExec.Fetch(ctx)
				gitExec.Checkout(ctx, pr.HeadRef)
			}
			if err := gitExec.Pull(ctx, pr.HeadRef); err != nil {
				log.Printf("pull 失败: %v", err)
			}
			return repoDir, nil
		}
	}

	// 全新克隆
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return "", fmt.Errorf("create repo dir: %w", err)
	}

	gitExec := git.NewExecutor(repoDir)
	if _, err := gitExec.Clone(ctx, pr.CloneURL, pr.HeadRef); err != nil {
		return "", fmt.Errorf("clone: %w", err)
	}

	return repoDir, nil
}

// submitReview 将审查结果提交到 GitHub PR
func (p *PRProcessor) submitReview(ctx context.Context, pr *github.PRInfo, result *model.ReviewResult) error {
	if len(result.Comments) == 0 {
		// 没有评论，仅提交总结
		return p.githubClient.CreateReview(pr.Owner, pr.Repo, pr.Number, github.PRReview{
			Body:  result.Summary,
			Event: "COMMENT",
		})
	}

	// 构建审查评论
	var reviewComments []github.ReviewComment
	for _, c := range result.Comments {
		body := formatCommentBody(c)
		reviewComments = append(reviewComments, github.ReviewComment{
			Body: body,
			Path: c.FilePath,
			Line: c.Line,
			Side: "RIGHT",
		})
	}

	// 确定审查结论
	event := "COMMENT"
	hasCritical := false
	for _, c := range result.Comments {
		if c.Severity == "critical" {
			hasCritical = true
			break
		}
	}
	if hasCritical {
		event = "REQUEST_CHANGES"
	}

	return p.githubClient.CreateReview(pr.Owner, pr.Repo, pr.Number, github.PRReview{
		Body:     result.Summary,
		Event:    event,
		Comments: reviewComments,
	})
}

// formatCommentBody 格式化单条评论内容
func formatCommentBody(c model.ReviewComment) string {
	emoji := map[string]string{
		"critical": "🔴",
		"warning":  "🟡",
		"info":     "🔵",
	}

	body := fmt.Sprintf("**%s [%s/%s]**\n\n%s",
		emoji[c.Severity], c.Category, c.Severity, c.Message)

	if c.Suggestion != "" {
		body += fmt.Sprintf("\n\n**建议修复:**\n```suggestion\n%s\n```", c.Suggestion)
	}

	if c.RuleID != "" {
		body += fmt.Sprintf("\n\n> 规则: `%s` | 置信度: %.0f%%", c.RuleID, c.Confidence*100)
	}

	return body
}

// reportFailure 报告审查失败
func (p *PRProcessor) reportFailure(pr *github.PRInfo, reason string) {
	log.Printf("[PR #%d] 审查失败: %s", pr.Number, reason)
	p.githubClient.UpdatePRStatus(pr.Owner, pr.Repo, pr.HeadSHA,
		"error", reason, "woodpecker/review")
}

// ParseAndReview 直接解析 diff 文本并审查（不依赖 Git 仓库）
// 用于 Webhook 直接传入 diff 的场景
func (p *PRProcessor) ParseAndReview(ctx context.Context, diffText string) (*model.ReviewResult, error) {
	// 验证 diff 可解析
	_, err := diff.Parse(diffText)
	if err != nil {
		return nil, fmt.Errorf("parse diff: %w", err)
	}

	svcReq := service.ReviewRequest{
		DiffText: diffText,
		Language: "go",
	}

	svcResp := p.reviewer.Review(ctx, svcReq)
	if svcResp.Error != "" {
		return nil, fmt.Errorf("review: %s", svcResp.Error)
	}

	return svcResp.Result, nil
}
