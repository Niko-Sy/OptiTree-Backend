package middleware

import (
	"strings"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/util"
	jwtpkg "optitree-backend/pkg/jwt"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	ContextKeyUserID = "userId"
	ContextKeyRole   = "role"
	ContextKeyJTI    = "jti"
)

func JWTAuth(jwtManager *jwtpkg.Manager, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			util.FailUnauthorized(c)
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := jwtManager.ParseToken(tokenStr)
		if err != nil {
			if err == jwtpkg.ErrTokenExpired {
				util.Fail(c, constant.CodeTokenExpired, constant.MsgTokenExpired)
				c.Abort()
				return
			}
			util.FailUnauthorized(c)
			return
		}

		// 检查黑名单
		ctx := c.Request.Context()
		blacklistKey := constant.RedisKeyBlacklist + claims.JTI
		exists, err := rdb.Exists(ctx, blacklistKey).Result()
		if err == nil && exists > 0 {
			util.FailUnauthorized(c)
			return
		}

		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyRole, claims.Role)
		c.Set(ContextKeyJTI, claims.JTI)
		c.Next()
	}
}

// GetUserID 从 gin.Context 中获取用户ID
func GetUserID(c *gin.Context) string {
	if id, exists := c.Get(ContextKeyUserID); exists {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return ""
}

// GetRole 从 gin.Context 中获取用户角色
func GetRole(c *gin.Context) string {
	if role, exists := c.Get(ContextKeyRole); exists {
		if s, ok := role.(string); ok {
			return s
		}
	}
	return ""
}
