package http

import (
	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/fault"
	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/middleware/jwt"
	"feedsystem_video_go/internal/middleware/metrics"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/video"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
)

// SetFeedRouter 只挂载 feed 只读路由（微服务拆分后的 feed-api 入口）。
// 与单体 router 的区别：不挂 account/video/like/comment/social 路由。
func SetFeedRouter(db *gorm.DB, cache *rediscache.Client) *gin.Engine {
	r := gin.Default()

	// Prometheus 指标中间件（全局）+ metrics 端点
	r.Use(metrics.HttpMetricsMiddleware())
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// 故障注入：内存态开关 + debug 路由
	injector := fault.NewInjector()
	registerFaultRoutes(r, injector)

	// account：仅用于 JWT 鉴权（读 account 表）
	accountRepository := account.NewAccountRepository(db)
	// video：仅用于点赞状态（buildFeedVideos 里的 isLiked）
	likeRepository := video.NewLikeRepository(db)
	// social：仅用于关注流（GetFollowingIDs）
	socialRepository := social.NewSocialRepository(db)

	feedRepository := feed.NewFeedRepository(db)
	feedService := feed.NewFeedService(feedRepository, likeRepository, cache, socialRepository)
	feedHandler := feed.NewFeedHandler(feedService)

	feedGroup := r.Group("/feed")
	feedGroup.Use(jwt.SoftJWTAuth(accountRepository, cache))
	feedGroup.Use(slowSQLMiddleware(db, injector))
	feedGroup.Use(cacheBreakdownMiddleware(cache, injector))
	{
		feedGroup.POST("/list", feedHandler.FetchFeeds)
		feedGroup.POST("/listLatest", feedHandler.ListLatest)
		feedGroup.POST("/listLikesCount", feedHandler.ListLikesCount)
		feedGroup.POST("/listByPopularity", feedHandler.ListByPopularity)
	}
	protectedFeedGroup := feedGroup.Group("")
	protectedFeedGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedFeedGroup.POST("/listByFollowing", feedHandler.ListByFollowing)
	}
	return r
}
