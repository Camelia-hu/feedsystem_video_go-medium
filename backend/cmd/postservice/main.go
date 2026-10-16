package main

import (
	"context"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	apphttp "feedsystem_video_go/internal/http"
	rabbitmq "feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/video"
	"feedsystem_video_go/internal/worker"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	socialQueue  = "social.events"
	likeQueue    = "like.events"
	commentQueue = "comment.events"
	videoQueue   = "video.box.events"
)

func main() {
	log.Printf("post-service: loading config")
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("post-service: load config: %v", err)
	}

	// 连接数据库（post-service 拥有表结构迁移）
	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("post-service: connect db: %v", err)
	}
	if err := db.AutoMigrate(sqlDB); err != nil {
		log.Fatalf("post-service: auto migrate: %v", err)
	}
	defer db.CloseDB(sqlDB)

	// 连接 Redis（可选，用于缓存/流行度）
	cache, err := rediscache.NewFromEnv(&cfg.Redis)
	if err != nil {
		log.Printf("post-service: redis disabled: %v", err)
		cache = nil
	} else {
		pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := cache.Ping(pingCtx); err != nil {
			log.Printf("post-service: redis disabled: %v", err)
			_ = cache.Close()
			cache = nil
		} else {
			defer cache.Close()
			log.Printf("post-service: redis connected")
		}
	}

	// 生产者连接（API 写路径发布 MQ 事件）
	rmq, err := rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
	if err != nil {
		log.Fatalf("post-service: rabbitmq producer: %v", err)
	}
	defer rmq.Close()

	// 先建路由：SetPostRouter 内部通过各 MQ wrapper 声明 exchange/queue/binding（幂等）
	r := apphttp.SetPostRouter(sqlDB, cache, rmq)

	// 消费者连接（4 个 worker）
	url := "amqp://" + cfg.RabbitMQ.Username + ":" + cfg.RabbitMQ.Password + "@" + cfg.RabbitMQ.Host + ":" + strconv.Itoa(cfg.RabbitMQ.Port) + "/"
	conn, err := amqp.Dial(url)
	if err != nil {
		log.Fatalf("post-service: rabbitmq consumer: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("post-service: rabbitmq channel: %v", err)
	}
	defer ch.Close()
	if err := ch.Qos(50, 0, false); err != nil {
		log.Fatalf("post-service: set qos: %v", err)
	}

	socialRepo := social.NewSocialRepository(sqlDB)
	videoRepo := video.NewVideoRepository(sqlDB)
	likeRepo := video.NewLikeRepository(sqlDB)
	commentRepo := video.NewCommentRepository(sqlDB)

	socialWorker := worker.NewSocialWorker(ch, socialRepo, socialQueue)
	likeWorker := worker.NewLikeWorker(ch, likeRepo, videoRepo, likeQueue)
	commentWorker := worker.NewCommentWorker(ch, commentRepo, videoRepo, commentQueue)
	videoWorker := worker.NewVideoWorker(ch, videoRepo, socialRepo, cache, videoQueue, 1000)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 5)
	go func() { errCh <- socialWorker.Run(ctx) }()
	go func() { errCh <- likeWorker.Run(ctx) }()
	go func() { errCh <- commentWorker.Run(ctx) }()
	go func() { errCh <- videoWorker.Run(ctx) }()
	log.Printf("post-service: workers started (social/like/comment/video)")

	go func() {
		log.Printf("post-service: server running on port %d", cfg.Server.Port)
		if err := r.Run(":" + strconv.Itoa(cfg.Server.Port)); err != nil {
			errCh <- err
		}
	}()

	err = <-errCh
	if err != nil && err != context.Canceled {
		log.Fatalf("post-service: stopped: %v", err)
	}
	log.Printf("post-service: stopped")
}
