package worker

import (
	"context"
	"encoding/json"
	"errors"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/video"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
	amqp "github.com/rabbitmq/amqp091-go"
)

type VideoWorker struct {
	ch         *amqp.Channel
	videoRepo  *video.VideoRepository
	socialRepo *social.SocialRepository
	queue      string
}

func NewVideoWorker(ch *amqp.Channel, videoRepo *video.VideoRepository, socialRepo *social.SocialRepository, queue string) *VideoWorker {
	return &VideoWorker{ch: ch, videoRepo: videoRepo, socialRepo: socialRepo, queue: queue}
}

func (w *VideoWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.videoRepo == nil || w.socialRepo == nil {
		return errors.New("video worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}

	deliveries, err := w.ch.Consume(
		w.queue,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("deliveries channel closed")
			}
			w.handleDelivery(ctx, d)
		}
	}
}

func (w *VideoWorker) handleDelivery(ctx context.Context, d amqp.Delivery) {
	if err := w.process(ctx, d.Body); err != nil {
		log.Printf("video worker: failed to process message: %v", err)
		// 重新入队，稍后重试
		_ = d.Nack(false, true)
		return
	}
	_ = d.Ack(false)
}

func (w *VideoWorker) process(ctx context.Context, body []byte) error {
	var evt rabbitmq.VideoEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		// 消息格式错误，直接丢弃
		return nil
	}
	if evt.VideoID == 0 || evt.AuthorID == 0 {
		return nil
	}

	switch evt.Action {
	case "publish_inbox":
		const batchSize = 500
		score := time.Now().UnixMilli()
		var lastRelationID uint
		for {
			followers, nextCursor, err := w.socialRepo.GetFollowersBatch(ctx, evt.AuthorID, lastRelationID, batchSize)
			if err != nil {
				return err
			}
			if len(followers) == 0 {
				break
			}

			err = w.videoRepo.BatchInsertFollowFeedInbox(ctx, followers, int64(evt.VideoID), int64(evt.AuthorID), score)
			if err != nil {
				var mysqlErr *mysql.MySQLError
				if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
					// 唯一键冲突，说明已经插过了，幂等处理
					return nil
				}
				return err
			}

			if len(followers) < batchSize {
				break
			}
			lastRelationID = nextCursor
		}
		return nil

	case "publish_outbox":
		err := w.videoRepo.InsertFeedOutbox(ctx, int64(evt.AuthorID), int64(evt.VideoID), time.Now().UnixMilli())
		if err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				// 唯一键冲突，说明已经插过了，幂等处理
				return nil
			}
			return err
		}
		return nil
	default:
		return nil
	}
}
