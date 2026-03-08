package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		duration := time.Since(start)
		statusCode := c.Writer.Status()
		requestID, _ := c.Get("requestId")
		userID := GetUserID(c)

		event := log.Info()
		if statusCode >= http.StatusInternalServerError {
			event = log.Error()
		} else if statusCode >= http.StatusBadRequest {
			event = log.Warn()
		}

		logEvent := event.
			Str("requestId", func() string {
				if s, ok := requestID.(string); ok {
					return s
				}
				return ""
			}()).
			Str("method", c.Request.Method).
			Str("path", path).
			Str("query", query).
			Int("status", statusCode).
			Dur("duration", duration).
			Str("ip", c.ClientIP())

		if userID != "" {
			logEvent = logEvent.Str("userId", userID)
		}
		if len(c.Errors) > 0 {
			logEvent = logEvent.Str("errors", c.Errors.String())
		}

		logEvent.Msg("request")
	}
}
