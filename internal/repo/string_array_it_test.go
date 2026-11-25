//go:build integration

package repo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

// TestDocumentIgnoreRulesTextArrayIT 端到端验证抓到的缺陷回归（SQLSTATE 42804）：
// GORM 写 []string 到 text[] 列会生成 RECORD 字面量而非数组字面量。StringArray 的
// Valuer/Scanner 修复后，非空 ignore_rules 必须可落库并往返读回。
func TestDocumentIgnoreRulesTextArrayIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "git-in")

	rules := model.StringArray{".github/*", "*.tmp", "with space"}
	doc := &model.Document{
		KBID: docs[0].KBID, Name: "it-ignore-rules", Type: model.DocTypeGit,
		Status: model.DocStatusPending, RepoURL: &docs[0].Name, IgnoreRules: rules,
		CreatedBy: docs[0].CreatedBy,
	}
	require.NoError(t, r.CreateDocument(ctx, doc), "非空 ignore_rules 必须可写入 text[] 列")

	got, err := r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, rules, got.IgnoreRules, "text[] 列必须往返读回一致")
}
