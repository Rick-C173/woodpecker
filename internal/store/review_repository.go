package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"woodpecker/internal/model"
)

type ReviewRepository struct {
	db *pgxpool.Pool
}

func NewReviewRepository(db *pgxpool.Pool) *ReviewRepository {
	return &ReviewRepository{db: db}
}

type Project struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	Owner         string    `json:"owner"`
	Repo          string    `json:"repo"`
	WebhookSecret string    `json:"webhook_secret,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Review struct {
	ID               int       `json:"id"`
	ProjectID        int       `json:"project_id"`
	PRNumber         int       `json:"pr_number"`
	PRTitle          string    `json:"pr_title"`
	PRURL            string    `json:"pr_url"`
	CommitSHA        string    `json:"commit_sha"`
	Branch           string    `json:"branch"`
	BaseBranch       string    `json:"base_branch"`
	DiffText         string    `json:"diff_text,omitempty"`
	Language         string    `json:"language"`
	Summary          string    `json:"summary"`
	TotalFiles       int       `json:"total_files"`
	TotalIssues      int       `json:"total_issues"`
	Status           string    `json:"status"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	ReviewerType     string    `json:"reviewer_type"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (r *ReviewRepository) SaveReview(ctx context.Context, review *Review) (int, error) {
	query := `
		INSERT INTO reviews (
			project_id, pr_number, pr_title, pr_url, commit_sha, branch, base_branch,
			diff_text, language, summary, total_files, total_issues, status,
			error_message, prompt_tokens, completion_tokens, reviewer_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		RETURNING id
	`

	var id int
	err := r.db.QueryRow(ctx, query,
		review.ProjectID, review.PRNumber, review.PRTitle, review.PRURL,
		review.CommitSHA, review.Branch, review.BaseBranch, review.DiffText,
		review.Language, review.Summary, review.TotalFiles, review.TotalIssues,
		review.Status, review.ErrorMessage, review.PromptTokens, review.CompletionTokens,
		review.ReviewerType,
	).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf("save review: %w", err)
	}

	return id, nil
}

func (r *ReviewRepository) UpdateReview(ctx context.Context, id int, review *Review) error {
	query := `
		UPDATE reviews SET
			summary = $2,
			total_files = $3,
			total_issues = $4,
			status = $5,
			error_message = $6,
			prompt_tokens = $7,
			completion_tokens = $8,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

	_, err := r.db.Exec(ctx, query,
		id, review.Summary, review.TotalFiles, review.TotalIssues,
		review.Status, review.ErrorMessage, review.PromptTokens, review.CompletionTokens,
	)
	return err
}

func (r *ReviewRepository) SaveComment(ctx context.Context, reviewID int, comment *model.ReviewComment) error {
	query := `
		INSERT INTO review_comments (
			review_id, file_path, line_number, category, severity,
			message, suggestion, confidence, rule_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.Exec(ctx, query,
		reviewID, comment.FilePath, comment.Line, comment.Category,
		comment.Severity, comment.Message, comment.Suggestion,
		comment.Confidence, comment.RuleID,
	)
	return err
}

func (r *ReviewRepository) GetReviewByID(ctx context.Context, id int) (*Review, error) {
	query := `
		SELECT id, project_id, pr_number, pr_title, pr_url, commit_sha, branch,
			   base_branch, language, summary, total_files, total_issues, status,
			   error_message, prompt_tokens, completion_tokens, reviewer_type,
			   created_at, updated_at
		FROM reviews WHERE id = $1
	`

	var review Review
	err := r.db.QueryRow(ctx, query, id).Scan(
		&review.ID, &review.ProjectID, &review.PRNumber, &review.PRTitle,
		&review.PRURL, &review.CommitSHA, &review.Branch, &review.BaseBranch,
		&review.Language, &review.Summary, &review.TotalFiles, &review.TotalIssues,
		&review.Status, &review.ErrorMessage, &review.PromptTokens,
		&review.CompletionTokens, &review.ReviewerType, &review.CreatedAt,
		&review.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &review, nil
}

func (r *ReviewRepository) GetCommentsByReviewID(ctx context.Context, reviewID int) ([]model.ReviewComment, error) {
	query := `
		SELECT file_path, line_number, category, severity, message,
			   suggestion, confidence, rule_id
		FROM review_comments WHERE review_id = $1 ORDER BY id
	`

	rows, err := r.db.Query(ctx, query, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []model.ReviewComment
	for rows.Next() {
		var c model.ReviewComment
		err := rows.Scan(&c.FilePath, &c.Line, &c.Category, &c.Severity,
			&c.Message, &c.Suggestion, &c.Confidence, &c.RuleID)
		if err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}

type ListReviewsFilter struct {
	ProjectID *int
	PRNumber  *int
	CommitSHA string
	Status    string
	Limit     int
	Offset    int
}

func (r *ReviewRepository) ListReviews(ctx context.Context, filter ListReviewsFilter) ([]Review, error) {
	query := `
		SELECT id, project_id, pr_number, pr_title, pr_url, commit_sha, branch,
			   base_branch, language, summary, total_files, total_issues, status,
			   error_message, prompt_tokens, completion_tokens, reviewer_type,
			   created_at, updated_at
		FROM reviews WHERE 1=1
	`
	args := []any{}
	argIdx := 1

	if filter.ProjectID != nil {
		query += fmt.Sprintf(" AND project_id = $%d", argIdx)
		args = append(args, *filter.ProjectID)
		argIdx++
	}
	if filter.PRNumber != nil {
		query += fmt.Sprintf(" AND pr_number = $%d", argIdx)
		args = append(args, *filter.PRNumber)
		argIdx++
	}
	if filter.CommitSHA != "" {
		query += fmt.Sprintf(" AND commit_sha = $%d", argIdx)
		args = append(args, filter.CommitSHA)
		argIdx++
	}
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, filter.Status)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, filter.Limit)
		argIdx++
	}
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, filter.Offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []Review
	for rows.Next() {
		var review Review
		err := rows.Scan(
			&review.ID, &review.ProjectID, &review.PRNumber, &review.PRTitle,
			&review.PRURL, &review.CommitSHA, &review.Branch, &review.BaseBranch,
			&review.Language, &review.Summary, &review.TotalFiles, &review.TotalIssues,
			&review.Status, &review.ErrorMessage, &review.PromptTokens,
			&review.CompletionTokens, &review.ReviewerType, &review.CreatedAt,
			&review.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, review)
	}
	return reviews, rows.Err()
}

func (r *ReviewRepository) SaveProject(ctx context.Context, project *Project) (int, error) {
	query := `
		INSERT INTO projects (name, owner, repo, webhook_secret)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (owner, repo) DO UPDATE SET
			name = EXCLUDED.name,
			webhook_secret = EXCLUDED.webhook_secret,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`

	var id int
	err := r.db.QueryRow(ctx, query, project.Name, project.Owner, project.Repo, project.WebhookSecret).Scan(&id)
	return id, err
}

func (r *ReviewRepository) GetProjectByOwnerRepo(ctx context.Context, owner, repo string) (*Project, error) {
	query := `SELECT id, name, owner, repo, webhook_secret, created_at, updated_at
		FROM projects WHERE owner = $1 AND repo = $2`

	var project Project
	err := r.db.QueryRow(ctx, query, owner, repo).Scan(
		&project.ID, &project.Name, &project.Owner, &project.Repo,
		&project.WebhookSecret, &project.CreatedAt, &project.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &project, nil
}

func (r *ReviewRepository) GetProjects(ctx context.Context) ([]Project, error) {
	query := `SELECT id, name, owner, repo, webhook_secret, created_at, updated_at
		FROM projects ORDER BY updated_at DESC`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		err := rows.Scan(&p.ID, &p.Name, &p.Owner, &p.Repo, &p.WebhookSecret, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (r *ReviewRepository) GetReviewStats(ctx context.Context, projectID *int) (map[string]any, error) {
	query := `
		SELECT 
			COUNT(*) as total_reviews,
			COUNT(CASE WHEN status = 'success' THEN 1 END) as successful_reviews,
			COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed_reviews,
			COALESCE(SUM(total_issues), 0) as total_issues,
			COALESCE(SUM(prompt_tokens + completion_tokens), 0) as total_tokens
		FROM reviews
	`
	args := []any{}
	if projectID != nil {
		query += " WHERE project_id = $1"
		args = append(args, *projectID)
	}

	var stats struct {
		TotalReviews      int `json:"total_reviews"`
		SuccessfulReviews int `json:"successful_reviews"`
		FailedReviews     int `json:"failed_reviews"`
		TotalIssues       int `json:"total_issues"`
		TotalTokens       int `json:"total_tokens"`
	}

	err := r.db.QueryRow(ctx, query, args...).Scan(
		&stats.TotalReviews, &stats.SuccessfulReviews,
		&stats.FailedReviews, &stats.TotalIssues, &stats.TotalTokens,
	)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"total_reviews":      stats.TotalReviews,
		"successful_reviews": stats.SuccessfulReviews,
		"failed_reviews":     stats.FailedReviews,
		"total_issues_found": stats.TotalIssues,
		"total_tokens_used":  stats.TotalTokens,
	}, nil
}
