package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

const (
	CtxKBID   = "kb_id"
	CtxKBRole = "kb_role" // int16；0 表示管理员越过成员资格
)

// KBLookup 知识库成员角色查询（*repo.KBRepo 满足该接口）。
type KBLookup interface {
	GetMemberRole(ctx context.Context, kbID, userID int64) (int16, error)
}

// KBAccess 解析路径参数 :kb 并校验成员资格（PRD R6 不可见原则）：
//   - 非成员且非管理员 → 404（不泄露库名）
//   - 管理员放行；若管理员不是成员，可通过 onAdminBypass 记审计
func KBAccess(lookup KBLookup, onAdminBypass func(c *gin.Context, userID, kbID int64)) gin.HandlerFunc {
	return func(c *gin.Context) {
		kbID, err := strconv.ParseInt(c.Param("kb"), 10, 64)
		if err != nil || kbID <= 0 {
			httpx.AbortFailWith(c, http.StatusNotFound, httpx.CodeNotFound, "知识库不存在")
			return
		}
		userID := UserID(c)
		isAdmin, _ := c.Get(CtxIsAdmin)
		role, err := lookup.GetMemberRole(c.Request.Context(), kbID, userID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			httpx.AbortFailWith(c, http.StatusInternalServerError, "INTERNAL", "服务内部错误")
			return
		}
		isMember := err == nil
		if !isMember && isAdmin != true {
			httpx.AbortFailWith(c, http.StatusNotFound, httpx.CodeNotFound, "知识库不存在")
			return
		}
		c.Set(CtxKBID, kbID)
		if isAdmin == true {
			c.Set(CtxKBRole, int16(0)) // 管理员无论是否为成员都按最高权限处理
			if !isMember && onAdminBypass != nil {
				onAdminBypass(c, userID, kbID)
			}
		} else {
			c.Set(CtxKBRole, role)
		}
		c.Next()
	}
}

// RequireKBEditor 库内编辑者及以上（接入/上传/重试/同步）；管理员已由 KBAccess 放行。
func RequireKBEditor() gin.HandlerFunc {
	return requireKBRole(model.KBRoleEditor, "无权限执行该操作（需编辑者及以上）")
}

// RequireKBOwner 库内管理者（删除文档/重建索引）；管理员已由 KBAccess 放行。
func RequireKBOwner() gin.HandlerFunc {
	return requireKBRole(model.KBRoleOwner, "无权限执行该操作（需管理者）")
}

func requireKBRole(minRole int16, message string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := KBRole(c)
		if role == 0 || role <= minRole { // 0=管理员越权放行
			c.Next()
			return
		}
		httpx.AbortFailWith(c, http.StatusForbidden, httpx.CodeForbidden, message)
	}
}

// KBRole 当前用户在库内角色（0=管理员）。
func KBRole(c *gin.Context) int16 {
	if v, ok := c.Get(CtxKBRole); ok {
		if r, ok := v.(int16); ok {
			return r
		}
	}
	return 0
}

// KBID 当前请求解析出的知识库 ID。
func KBID(c *gin.Context) int64 {
	if v, ok := c.Get(CtxKBID); ok {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}
