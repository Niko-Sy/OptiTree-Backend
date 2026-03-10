package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSLocalhost 仅允许来自 localhost 的跨域请求，用于本地开发联调。
func CORSLocalhost() gin.HandlerFunc {
	return func(c *gin.Context) {
		// origin := c.GetHeader("Origin")
		// if origin == "" {
		// 	c.Next()
		// 	return
		// }

		// parsed, err := url.Parse(origin)
		// if err != nil || !isLocalhost(parsed.Hostname()) {
		// 	c.Next()
		// 	return
		// }

		headers := c.Writer.Header()
		// headers.Set("Access-Control-Allow-Origin", origin)
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Credentials", "true")
		headers.Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		headers.Set("Access-Control-Expose-Headers", "X-Request-Id")
		headers.Set("Access-Control-Max-Age", "600")
		appendVary(headers, "Origin")

		requestedHeaders := c.GetHeader("Access-Control-Request-Headers")
		if strings.TrimSpace(requestedHeaders) == "" {
			requestedHeaders = "Authorization,Content-Type,X-Request-Id"
		}
		headers.Set("Access-Control-Allow-Headers", requestedHeaders)
		appendVary(headers, "Access-Control-Request-Headers")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func isLocalhost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func appendVary(headers http.Header, key string) {
	current := headers.Get("Vary")
	if current == "" {
		headers.Set("Vary", key)
		return
	}
	for _, item := range strings.Split(current, ",") {
		if strings.EqualFold(strings.TrimSpace(item), key) {
			return
		}
	}
	headers.Set("Vary", current+", "+key)
}
