package main

import (
	"log"
	"os"
	"time"

	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	"feedsystem_video_go/internal/pay"

	"github.com/gin-gonic/gin"
)

func main() {
	port := os.Getenv("PAY_GATEWAY_PORT")
	if port == "" {
		port = "8090"
	}
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if err := db.AutoMigrate(sqlDB); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	cashier := pay.NewCashierWithDB(os.Getenv("PAY_CALLBACK_SECRET"), sqlDB)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			cashier.RetryNotifies()
		}
	}()
	r := gin.Default()
	cashier.Register(r)
	log.Printf("pay gateway listening on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
