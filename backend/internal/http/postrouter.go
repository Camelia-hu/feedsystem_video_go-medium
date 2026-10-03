package http

import (
	"feedsystem_video_go/internal/account"
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

// SetPostRouter 挂载写路径路由（微服务拆分后的 post-service 入口）：
// account / video / like / comment / social。不含 feed 读路由。
// agent-worker 暂缓：orchestrationMQ 传 nil，发布视频时不触发 LLM 分析。
func SetPostRouter(db *gorm.DB, cache *rediscache.Client, rmq *rabbitmq.RabbitMQ) *gin.Engine {
	r := gin.Default()

	r.Use(metrics.HttpMetricsMiddleware())
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// 静态上传文件服务
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

	// video（写路径）+ Agentic 发布工作流的 human-in-the-loop 接口
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
	suggestionRepository := video.NewAISuggestionRepository(db)
	// agent-worker 暂缓：不接 orchestrationMQ
	videoService := video.NewVideoService(videoRepository, suggestionRepository, videoL1, cache, popularityMQ, videoMQ, nil)
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
		protectedVideoGroup.GET("/:id/ai-suggestion", aiSuggestionHandler.GetSuggestion)
		protectedVideoGroup.POST("/:id/ai-suggestion/confirm", aiSuggestionHandler.ConfirmSuggestion)
		protectedVideoGroup.POST("/:id/ai-suggestion/reject", aiSuggestionHandler.RejectSuggestion)
	}
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

	return r
}
