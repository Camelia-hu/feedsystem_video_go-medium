package main

import (
	"context"
	"feedsystem_video_go/internal/config"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/worker"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	popularityExchange   = "video.popularity.events"
	popularityQueue      = "video.popularity.events"
	popularityBindingKey = "video.popularity.*"
)

func main() {
	log.Printf("rank-worker: loading config")
	cfg, err := config.Load("configs/config.yaml")
	if err != nil {
		log.Fatalf("rank-worker: load config: %v", err)
	}

	// Redis（流行度 ZSET 必须）
	cache, err := rediscache.NewFromEnv(&cfg.Redis)
	if err != nil {
		log.Fatalf("rank-worker: redis required: %v", err)
	}
	defer cache.Close()
	{
		pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := cache.Ping(pingCtx); err != nil {
			log.Fatalf("rank-worker: redis required: %v", err)
		}
	}
	log.Printf("rank-worker: redis connected")

	// RabbitMQ 消费者
	url := "amqp://" + cfg.RabbitMQ.Username + ":" + cfg.RabbitMQ.Password + "@" + cfg.RabbitMQ.Host + ":" + strconv.Itoa(cfg.RabbitMQ.Port) + "/"
	conn, err := amqp.Dial(url)
	if err != nil {
		log.Fatalf("rank-worker: rabbitmq: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("rank-worker: channel: %v", err)
	}
	defer ch.Close()

	if err := declarePopularityTopology(ch); err != nil {
		log.Fatalf("rank-worker: declare topology: %v", err)
	}
	if err := ch.Qos(50, 0, false); err != nil {
		log.Fatalf("rank-worker: set qos: %v", err)
	}

	popularityWorker := worker.NewPopularityWorker(ch, cache, popularityQueue)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// metrics + healthz HTTP server（供 ongrid 抓取）
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	go func() {
		log.Printf("rank-worker: metrics server on :%d", cfg.Server.Port)
		if err := http.ListenAndServe(":"+strconv.Itoa(cfg.Server.Port), mux); err != nil {
			log.Printf("rank-worker: metrics server: %v", err)
		}
	}()

	log.Printf("rank-worker: consuming queue=%s", popularityQueue)
	if err := popularityWorker.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("rank-worker: stopped: %v", err)
	}
	log.Printf("rank-worker: stopped")
}

func declarePopularityTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(popularityExchange, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	q, err := ch.QueueDeclare(popularityQueue, true, false, false, false, nil)
	if err != nil {
		return err
	}
	return ch.QueueBind(q.Name, popularityBindingKey, popularityExchange, false, nil)
}
