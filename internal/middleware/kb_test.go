package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

type fakeKBLookup struct {
	roles map[[2]int64]int16
	vis   map[int64]int16 // kbID → visibility（未登记=私有）
	err   error
}

func (f *fakeKBLookup) GetMemberRole(_ context.Context, kbID, userID int64) (int16, error) {
	if f.err != nil {
		return 0, f.err
	}
	if r, ok := f.roles[[2]int64{kbID, userID}]; ok {
		return r, nil
	}
	return 0, repo.ErrNotFound
}

// GetVisibility 未登记的库视为私有（FR-17：默认值语义）。
func (f *fakeKBLookup) GetVisibility(_ context.Context, kbID int64) (int16, error) {
	if f.err != nil {
		return 0, f.err
	}
	if v, ok := f.vis[kbID]; ok {
		return v, nil
	}
	return model.KBVisibilityPrivate, nil
}

// GetEffectiveRole FR-18：测试替身直接返回 GetMemberRole 语义（单测不覆盖组合并，见 IT）。
func (f *fakeKBLookup) GetEffectiveRole(ctx context.Context, kbID, userID int64) (int16, error) {
	return f.GetMemberRole(ctx, kbID, userID)
}

func kbRouter(lookup KBLookup, userID int64, isAdmin bool, bypass func(*gin.Context, int64, int64)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(CtxUserID, userID)
		c.Set(CtxIsAdmin, isAdmin)
		c.Next()
	})
	r.GET("/kbs/:kb", KBAccess(lookup, bypass), func(c *gin.Context) {
		c.JSON(200, gin.H{"kb": KBID(c), "role": KBRole(c)})
	})
	return r
}

func TestKBAccess(t *testing.T) {
	lookup := &fakeKBLookup{roles: map[[2]int64]int16{
		{1, 7}: model.KBRoleEditor,
		{1, 8}: model.KBRoleMember,
	}, vis: map[int64]int16{
		2: model.KBVisibilityOrg, // kb2=全员可见
	}}

	// 非法 kb 参数 → 404
	r := kbRouter(lookup, 7, false, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/abc", nil))
	assert.Equal(t, 404, w.Code)
	assert.Contains(t, w.Body.String(), "NOT_FOUND")

	// kbID <= 0 边界同样 404（非数字之外的字面量 0 / 负数）
	for _, bad := range []string{"0", "-1"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/"+bad, nil))
		assert.Equal(t, 404, w.Code, "kb 参数 %s 应 404", bad)
		assert.Contains(t, w.Body.String(), "NOT_FOUND")
	}

	// 管理员 + kbID=0 → 仍 404（边界防线不可被管理员越权穿透）
	ra := kbRouter(lookup, 99, true, nil)
	w = httptest.NewRecorder()
	ra.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/0", nil))
	assert.Equal(t, 404, w.Code)

	// 非成员 → 404（不可见原则）
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/3", nil))
	assert.Equal(t, 404, w.Code)
	assert.NotContains(t, w.Body.String(), "kb_name")

	// FR-17：全员可见库非成员 → 放行且等效 member 只读
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/2", nil))
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"kb":2,"role":3}`, w.Body.String())

	// FR-17：org 库非成员不可获得成员管理权（KBMemberManager 拦截 member 角色）
	gin.SetMode(gin.TestMode)
	mr := gin.New()
	mr.Use(func(c *gin.Context) {
		c.Set(CtxUserID, 7)
		c.Set(CtxIsAdmin, false)
		c.Set(CtxKBID, int64(2))
		c.Set(CtxKBRole, model.KBRoleMember)
		c.Next()
	})
	mr.POST("/members", KBMemberManager(), func(c *gin.Context) { c.Status(200) })
	mw := httptest.NewRecorder()
	mr.ServeHTTP(mw, httptest.NewRequest("POST", "/members", nil))
	assert.Equal(t, 403, mw.Code)
	assert.Contains(t, mw.Body.String(), "FORBIDDEN")

	// 成员 → 放行并注入角色
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/1", nil))
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"kb":1,"role":2}`, w.Body.String())

	// 管理员非成员 → 放行（role=0）且触发审计回调
	bypassed := 0
	r = kbRouter(lookup, 99, true, func(*gin.Context, int64, int64) { bypassed++ })
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/1", nil))
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"kb":1,"role":0}`, w.Body.String())
	assert.Equal(t, 1, bypassed)

	// 管理员且是成员 → 仍按管理员（role=0），不记越权
	bypassed = 0
	r = kbRouter(lookup, 8, true, func(*gin.Context, int64, int64) { bypassed++ })
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/1", nil))
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"kb":1,"role":0}`, w.Body.String())
	assert.Equal(t, 0, bypassed)

	// 存储异常 → 500
	r = kbRouter(&fakeKBLookup{err: errors.New("db down")}, 7, false, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/kbs/1", nil))
	assert.Equal(t, 500, w.Code)
}

// TestKBRoleMatrix 四角色 × 编辑者/管理者门槛（R6 矩阵核心行）。
func TestKBRoleMatrix(t *testing.T) {
	cases := []struct {
		name   string
		role   int16 // 0=管理员越权
		editor int   // 访问 /kbs/1/edit 期望状态码
		owner  int
	}{
		{"管理员", 0, 200, 200},
		{"管理者", model.KBRoleOwner, 200, 200},
		{"编辑者", model.KBRoleEditor, 200, 403},
		{"成员", model.KBRoleMember, 403, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(CtxKBRole, tc.role); c.Next() })
			r.GET("/edit", RequireKBEditor(), func(c *gin.Context) { c.Status(200) })
			r.GET("/owner", RequireKBOwner(), func(c *gin.Context) { c.Status(200) })

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/edit", nil))
			assert.Equal(t, tc.editor, w.Code)
			if tc.editor == 403 {
				assert.Contains(t, w.Body.String(), "FORBIDDEN")
			}

			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/owner", nil))
			assert.Equal(t, tc.owner, w.Code)
		})
	}
}

func TestKBHelpersAbsent(t *testing.T) {
	c := testCtx()
	assert.Zero(t, KBRole(c))
	assert.Zero(t, KBID(c))
}
