package middleware

import (
	"net/http"
	"runtime/debug"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				requestID, _ := c.Get("requestId")
				log.Error().
					Interface("requestId", requestID).
					Interface("panic", r).
					Str("stack", string(debug.Stack())).
					Msg("panic recovered")

				c.JSON(http.StatusInternalServerError, util.ErrorResponse{
					Code:    constant.CodeServerError,
					Message: constant.MsgServerError,
					RequestID: func() string {
						if s, ok := requestID.(string); ok {
							return s
						}
						return ""
					}(),
				})
				c.Abort()
			}
		}()
		c.Next()
	}
}
