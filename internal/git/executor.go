package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Executor Git 命令执行器，封装常用 git 操作
type Executor struct {
	workDir string // 仓库工作目录
	timeout time.Duration
}

// NewExecutor 创建 Git 命令执行器
func NewExecutor(workDir string) *Executor {
	return &Executor{
		workDir: workDir,
		timeout: 30 * time.Second,
	}
}

// Clone 克隆仓库到本地
func (e *Executor) Clone(ctx context.Context, repoURL, branch string) (string, error) {
	if e.workDir == "" {
		return "", fmt.Errorf("workDir is not set")
	}

	args := []string{"clone"}
	if branch != "" {
		args = append(args, "-b", branch)
	}
	args = append(args, repoURL, ".")

	_, err := e.run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("clone %s: %w", repoURL, err)
	}

	return e.workDir, nil
}

// Fetch 拉取远程仓库最新代码
func (e *Executor) Fetch(ctx context.Context) error {
	_, err := e.run(ctx, "fetch", "origin")
	return err
}

// Checkout 切换分支
func (e *Executor) Checkout(ctx context.Context, branch string) error {
	_, err := e.run(ctx, "checkout", branch)
	return err
}

// Pull 拉取并合并远程代码
func (e *Executor) Pull(ctx context.Context, branch string) error {
	args := []string{"pull", "origin"}
	if branch != "" {
		args = append(args, branch)
	}
	_, err := e.run(ctx, args...)
	return err
}

// Diff 获取两个引用之间的差异
// baseRef 和 headRef 可以是 branch name / commit SHA / tag
func (e *Executor) Diff(ctx context.Context, baseRef, headRef string) (string, error) {
	// 使用三点语法：baseRef...headRef 表示从共同祖先到 headRef 的变更
	rangeSpec := fmt.Sprintf("%s...%s", baseRef, headRef)
	return e.run(ctx, "diff", rangeSpec)
}

// DiffUnified 获取统一格式的 diff（可指定上下文行数）
func (e *Executor) DiffUnified(ctx context.Context, baseRef, headRef string, contextLines int) (string, error) {
	rangeSpec := fmt.Sprintf("%s...%s", baseRef, headRef)
	return e.run(ctx, "diff", fmt.Sprintf("-U%d", contextLines), rangeSpec)
}

// DiffFiles 列出变更的文件名
func (e *Executor) DiffFiles(ctx context.Context, baseRef, headRef string) ([]string, error) {
	rangeSpec := fmt.Sprintf("%s...%s", baseRef, headRef)
	output, err := e.run(ctx, "diff", "--name-only", rangeSpec)
	if err != nil {
		return nil, err
	}

	files := strings.Split(strings.TrimSpace(output), "\n")
	var result []string
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			result = append(result, f)
		}
	}
	return result, nil
}

// GetLatestCommit 获取最新 commit SHA
func (e *Executor) GetLatestCommit(ctx context.Context, ref string) (string, error) {
	output, err := e.run(ctx, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// GetMergeBase 获取两个引用的共同祖先
func (e *Executor) GetMergeBase(ctx context.Context, ref1, ref2 string) (string, error) {
	output, err := e.run(ctx, "merge-base", ref1, ref2)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

// Log 获取提交日志
func (e *Executor) Log(ctx context.Context, ref string, n int) (string, error) {
	args := []string{"log", "--oneline", fmt.Sprintf("-%d", n)}
	if ref != "" {
		args = append(args, ref)
	}
	return e.run(ctx, args...)
}

// Status 获取仓库状态
func (e *Executor) Status(ctx context.Context) (string, error) {
	return e.run(ctx, "status", "--short")
}

// run 执行 git 命令
func (e *Executor) run(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = e.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s: timeout after %s", strings.Join(args, " "), e.timeout)
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}
