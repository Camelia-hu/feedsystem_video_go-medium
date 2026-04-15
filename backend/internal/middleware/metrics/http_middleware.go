package metrics

import (
	"time"

	"github.com/gin-gonic/gin"
)

// HttpMetricsMiddleware Gin 中间件，自动记录所有 HTTP 请求指标
func HttpMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// 处理请求
		c.Next()

		// 记录指标
		duration := time.Since(start)
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		method := c.Request.Method
		status := c.Writer.Status()

		RecordHttpRequest(method, path, status, duration)
	}
}
