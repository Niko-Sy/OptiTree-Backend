package middleware

import (
	"optitree-backend/internal/constant"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

// RequireProjectRole 要求用户在项目中具有最低角色权限
// minRole: "viewer" | "editor" | "admin"
func RequireProjectRole(minRole string, memberRepo *repository.MemberRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := GetUserID(c)
		if userID == "" {
			util.FailUnauthorized(c)
			return
		}

		projectID := c.Param("projectId")
		if projectID == "" {
			util.Fail(c, constant.CodeInvalidParam, "缺少 projectId")
			c.Abort()
			return
		}

		member, err := memberRepo.FindByProjectAndUser(projectID, userID)
		if err != nil {
			util.FailServerError(c)
			return
		}
		if member == nil {
			util.FailForbidden(c)
			return
		}

		userWeight := constant.RoleWeight[member.Role]
		minWeight := constant.RoleWeight[minRole]
		if userWeight < minWeight {
			util.FailForbidden(c)
			return
		}

		// 将成员角色写入 Context 方便后续使用
		c.Set("projectRole", member.Role)
		c.Next()
	}
}

// GetProjectRole 从 Context 中获取项目角色
func GetProjectRole(c *gin.Context) string {
	if role, exists := c.Get("projectRole"); exists {
		if s, ok := role.(string); ok {
			return s
		}
	}
	return ""
}
