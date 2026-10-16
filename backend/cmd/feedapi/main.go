package main

import (
	"context"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	apphttp "feedsystem_video_go/internal/http"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"log"
	"strconv"
	"time"
)

func main() {
	log.Printf("feed-api: loading config from configs/config.yaml (env overrides apply)")
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("feed-api: failed to load config: %v", err)
	}

	// 连接数据库
	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("feed-api: failed to connect database: %v", err)
	}
	// feed-api 也做 AutoMigrate（幂等），保证独立部署时表结构可用；
	// 与 post-service 的 AutoMigrate 是同一套 entity，重复执行无副作用。
	if err := db.AutoMigrate(sqlDB); err != nil {
		log.Fatalf("feed-api: failed to auto migrate database: %v", err)
	}
	defer db.CloseDB(sqlDB)

	// 连接 Redis（可选，用于缓存）
	cache, err := rediscache.NewFromEnv(&cfg.Redis)
	if err != nil {
		log.Printf("feed-api: redis config error (cache disabled): %v", err)
		cache = nil
	} else {
		pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := cache.Ping(pingCtx); err != nil {
			log.Printf("feed-api: redis not available (cache disabled): %v", err)
			_ = cache.Close()
			cache = nil
		} else {
			defer cache.Close()
			log.Printf("feed-api: redis connected (cache enabled)")
		}
	}

	r := apphttp.SetFeedRouter(sqlDB, cache)
	log.Printf("feed-api: server running on port %d", cfg.Server.Port)
	if err := r.Run(":" + strconv.Itoa(cfg.Server.Port)); err != nil {
		log.Fatalf("feed-api: failed to run server: %v", err)
	}
}
