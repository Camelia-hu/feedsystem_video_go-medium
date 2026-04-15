package http

import (
	"feedsystem_video_go/internal/account"
	"feedsystem_video_go/internal/feed"
	"feedsystem_video_go/internal/middleware/jwt"
	"feedsystem_video_go/internal/middleware/localcache"
	"feedsystem_video_go/internal/middleware/metrics"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/video"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"
)

func SetRouter(db *gorm.DB, cache *rediscache.Client, rmq *rabbitmq.RabbitMQ) *gin.Engine {
	r := gin.Default()

	// Prometheus 指标中间件（全局）
	r.Use(metrics.HttpMetricsMiddleware())

	// Prometheus metrics 端点
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.Static("/static", "./.run/uploads")

	// L1 进程内缓存（视频详情专用，32MB，TTL=1s）
	videoL1, err := localcache.New(0)
	if err != nil {
		log.Printf("localcache init failed (L1 disabled): %v", err)
		videoL1 = nil
	}
	// account
	accountRepository := account.NewAccountRepository(db)
	accountService := account.NewAccountService(accountRepository, cache)
	accountHandler := account.NewAccountHandler(accountService)
	accountGroup := r.Group("/account")
	{
		accountGroup.POST("/register", accountHandler.CreateAccount)
		accountGroup.POST("/login", accountHandler.Login)
		accountGroup.POST("/changePassword", accountHandler.ChangePassword)
		accountGroup.POST("/findByID", accountHandler.FindByID)
		accountGroup.POST("/findByUsername", accountHandler.FindByUsername)
	}
	protectedAccountGroup := accountGroup.Group("")
	protectedAccountGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedAccountGroup.POST("/logout", accountHandler.Logout)
		protectedAccountGroup.POST("/rename", accountHandler.Rename)
	}
	// video
	videoRepository := video.NewVideoRepository(db)
	popularityMQ, err := rabbitmq.NewPopularityMQ(rmq)
	if err != nil {
		log.Printf("PopularityMQ init failed (mq disabled): %v", err)
		popularityMQ = nil
	}
	videoMQ, err := rabbitmq.NewVideoMQ(rmq)
	if err != nil {
		log.Printf("VideoMQ init failed (mq disabled): %v", err)
		videoMQ = nil
	}
	orchestrationMQ, err := rabbitmq.NewOrchestrationMQ(rmq)
	if err != nil {
		log.Printf("OrchestrationMQ init failed (agent workflow disabled): %v", err)
		orchestrationMQ = nil
	}
	suggestionRepository := video.NewAISuggestionRepository(db)
	videoService := video.NewVideoService(videoRepository, suggestionRepository, videoL1, cache, popularityMQ, videoMQ, orchestrationMQ)
	videoHandler := video.NewVideoHandler(videoService, accountService)
	aiSuggestionHandler := video.NewAISuggestionHandler(videoService)
	videoGroup := r.Group("/video")
	{
		videoGroup.POST("/listByAuthorID", videoHandler.ListByAuthorID)
		videoGroup.POST("/getDetail", videoHandler.GetDetail)
	}
	protectedVideoGroup := videoGroup.Group("")
	protectedVideoGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedVideoGroup.POST("/uploadVideo", videoHandler.UploadVideo)
		protectedVideoGroup.POST("/uploadCover", videoHandler.UploadCover)
		protectedVideoGroup.POST("/publish", videoHandler.PublishVideo)
		protectedVideoGroup.POST("/delete", videoHandler.DeleteVideo)
		protectedVideoGroup.POST("/updateLikesCount", videoHandler.UpdateLikesCount)
		// Agentic 发布工作流：Human-in-the-loop 确认/拒绝 AI 建议
		protectedVideoGroup.GET("/:id/ai-suggestion", aiSuggestionHandler.GetSuggestion)
		protectedVideoGroup.POST("/:id/ai-suggestion/confirm", aiSuggestionHandler.ConfirmSuggestion)
		protectedVideoGroup.POST("/:id/ai-suggestion/reject", aiSuggestionHandler.RejectSuggestion)
	}
	// 管理员路由
	adminVideoGroup := videoGroup.Group("")
	adminVideoGroup.Use(jwt.JWTAuth(accountRepository, cache), jwt.AdminAuth(accountRepository))
	{
		adminVideoGroup.POST("/admin/delete", videoHandler.AdminDeleteVideo)
	}
	// like
	likeMQ, err := rabbitmq.NewLikeMQ(rmq)
	if err != nil {
		log.Printf("LikeMQ init failed (mq disabled): %v", err)
		likeMQ = nil
	}
	likeRepository := video.NewLikeRepository(db)
	likeService := video.NewLikeService(likeRepository, videoRepository, cache, likeMQ, popularityMQ)
	likeHandler := video.NewLikeHandler(likeService)
	likeGroup := r.Group("/like")
	protectedLikeGroup := likeGroup.Group("")
	protectedLikeGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedLikeGroup.POST("/like", likeHandler.Like)
		protectedLikeGroup.POST("/unlike", likeHandler.Unlike)
		protectedLikeGroup.POST("/isLiked", likeHandler.IsLiked)
		protectedLikeGroup.POST("/listMyLikedVideos", likeHandler.ListMyLikedVideos)
	}
	// comment
	commentRepository := video.NewCommentRepository(db)
	commentMQ, err := rabbitmq.NewCommentMQ(rmq)
	if err != nil {
		log.Printf("CommentMQ init failed (mq disabled): %v", err)
		commentMQ = nil
	}
	commentService := video.NewCommentService(commentRepository, videoRepository, cache, commentMQ, popularityMQ)
	commentHandler := video.NewCommentHandler(commentService, accountService)
	commentGroup := r.Group("/comment")
	{
		commentGroup.POST("/listAll", commentHandler.GetAllComments)
	}
	protectedCommentGroup := commentGroup.Group("")
	protectedCommentGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedCommentGroup.POST("/publish", commentHandler.PublishComment)
		protectedCommentGroup.POST("/delete", commentHandler.DeleteComment)
	}
	// 管理员路由
	adminCommentGroup := commentGroup.Group("")
	adminCommentGroup.Use(jwt.JWTAuth(accountRepository, cache), jwt.AdminAuth(accountRepository))
	{
		adminCommentGroup.POST("/admin/delete", commentHandler.AdminDeleteComment)
	}
	// social
	socialMQ, err := rabbitmq.NewSocialMQ(rmq)
	if err != nil {
		log.Printf("SocialMQ init failed (mq disabled): %v", err)
		socialMQ = nil
	}
	socialRepository := social.NewSocialRepository(db)
	socialService := social.NewSocialService(socialRepository, accountRepository, socialMQ)
	socialHandler := social.NewSocialHandler(socialService)
	socialGroup := r.Group("/social")
	protectedSocialGroup := socialGroup.Group("")
	protectedSocialGroup.Use(jwt.JWTAuth(accountRepository, cache))
	{
		protectedSocialGroup.POST("/follow", socialHandler.Follow)
		protectedSocialGroup.POST("/unfollow", socialHandler.Unfollow)
		protectedSocialGroup.POST("/getAllFollowers", socialHandler.GetAllFollowers)
		protectedSocialGroup.POST("/getAllVloggers", socialHandler.GetAllVloggers)
	}
	// feed
	feedRepository := feed.NewFeedRepository(db)
	feedService := feed.NewFeedService(feedRepository, likeRepository, cache, socialRepository)
	feedHandler := feed.NewFeedHandler(feedService)
	feedGroup := r.Group("/feed")
	feedGroup.Use(jwt.SoftJWTAuth(accountRepository, cache))
	{
		// 统一入口：通过 query_type 分派（latest/order_by_likes/order_by_popularity/followings）
		feedGroup.POST("/list", feedHandler.FetchFeeds)
		// 旧接口保留向后兼容
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
