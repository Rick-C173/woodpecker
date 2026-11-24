//go:build integration

// 摄取链路的真库集成测试公共前置（P1-8 / P1-9 / P1-10）。
package repo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

// itSeedDocKB 建一个知识库与若干上传文档；返回仓储、知识库与文档列表。
func itSeedDocKB(t *testing.T, names ...string) (*DocumentRepo, *model.KnowledgeBase, []*model.Document) {
	t.Helper()
	ctx := context.Background()
	kbs := NewKBRepo(itDB)
	users := NewUserRepo(itDB)
	owner := itSeedUser(t, users, "workerowner", true)
	kb := &model.KnowledgeBase{Name: "摄取库", CreatedBy: owner.ID}
	require.NoError(t, kbs.Create(ctx, kb))

	r := NewDocumentRepo(itDB)
	docs := make([]*model.Document, 0, len(names))
	for _, name := range names {
		d := &model.Document{KBID: kb.ID, Type: model.DocTypeUpload, Name: name,
			Status: model.DocStatusProcessing, CreatedBy: owner.ID}
		require.NoError(t, r.CreateDocument(ctx, d))
		docs = append(docs, d)
	}
	return r, kb, docs
}
