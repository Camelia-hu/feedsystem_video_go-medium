package http

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"feedsystem_video_go/internal/fault"
	rediscache "feedsystem_video_go/internal/middleware/redis"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// slowSQLMiddleware 故障注入：启用后在 feed 请求前执行 SELECT SLEEP(n)，拉高接口 P99。
func slowSQLMiddleware(db *gorm.DB, inj *fault.Injector) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enabled, seconds := inj.SlowSQL(); enabled && db != nil {
			sql := fmt.Sprintf("SELECT SLEEP(%d)", seconds)
			var ignored int
			if err := db.WithContext(c.Request.Context()).Raw(sql).Scan(&ignored).Error; err != nil {
				log.Printf("fault slow-sql: %v", err)
			}
		}
		c.Next()
	}
}

// cacheBreakdownMiddleware 故障注入：启用后删除 feed 缓存 key，制造缓存击穿（DB 负载上升）。
func cacheBreakdownMiddleware(cache *rediscache.Client, inj *fault.Injector) gin.HandlerFunc {
	return func(c *gin.Context) {
		if inj.CacheBreakdown() && cache != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if n, err := cache.DelByPattern(ctx, "feed:*"); err != nil {
				log.Printf("fault cache-breakdown: %v", err)
			} else if n > 0 {
				log.Printf("fault cache-breakdown: evicted %d keys", n)
			}
		}
		c.Next()
	}
}

// errorRateMiddleware 故障注入：按配置比例直接返回 500，拉高接口错误率（喂 5xx 错误率告警）。
func errorRateMiddleware(inj *fault.Injector) gin.HandlerFunc {
	return func(c *gin.Context) {
		if enabled, percent := inj.ErrorRate(); enabled && rand.IntN(100) < percent {
			c.AbortWithStatusJSON(500, gin.H{"error": "injected fault: error-rate"})
			return
		}
		c.Next()
	}
}

type slowSQLRequest struct {
	Enabled *bool `json:"enabled"`
	Seconds int   `json:"seconds"`
}

type toggleRequest struct {
	Enabled *bool `json:"enabled"`
}

type errorRateRequest struct {
	Enabled *bool `json:"enabled"`
	Percent int   `json:"percent"`
}

// registerFaultRoutes 注册故障注入 debug 路由（演示用，集群内可达）
func registerFaultRoutes(r *gin.Engine, inj *fault.Injector) {
	g := r.Group("/debug/fault")
	g.GET("", func(c *gin.Context) {
		slowOn, seconds := inj.SlowSQL()
		errorOn, percent := inj.ErrorRate()
		c.JSON(200, gin.H{
			"slow_sql":        gin.H{"enabled": slowOn, "seconds": seconds},
			"cache_breakdown": gin.H{"enabled": inj.CacheBreakdown()},
			"error_rate":      gin.H{"enabled": errorOn, "percent": percent},
		})
	})
	g.POST("/slow-sql", func(c *gin.Context) {
		var req slowSQLRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if req.Enabled == nil {
			c.JSON(400, gin.H{"error": "enabled is required"})
			return
		}
		if *req.Enabled {
			inj.EnableSlowSQL(req.Seconds)
		} else {
			inj.DisableSlowSQL()
		}
		slowOn, seconds := inj.SlowSQL()
		c.JSON(200, gin.H{"slow_sql": gin.H{"enabled": slowOn, "seconds": seconds}})
	})
	g.POST("/cache-breakdown", func(c *gin.Context) {
		var req toggleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if req.Enabled == nil {
			c.JSON(400, gin.H{"error": "enabled is required"})
			return
		}
		if *req.Enabled {
			inj.EnableCacheBreakdown()
		} else {
			inj.DisableCacheBreakdown()
		}
		c.JSON(200, gin.H{"cache_breakdown": gin.H{"enabled": inj.CacheBreakdown()}})
	})
	g.POST("/error-rate", func(c *gin.Context) {
		var req errorRateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if req.Enabled == nil {
			c.JSON(400, gin.H{"error": "enabled is required"})
			return
		}
		if *req.Enabled {
			inj.EnableErrorRate(req.Percent)
		} else {
			inj.DisableErrorRate()
		}
		errorOn, percent := inj.ErrorRate()
		c.JSON(200, gin.H{"error_rate": gin.H{"enabled": errorOn, "percent": percent}})
	})
}
