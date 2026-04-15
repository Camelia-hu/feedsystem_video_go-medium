package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"feedsystem_video_go/internal/middleware/metrics"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/video"

	"github.com/go-sql-driver/mysql"
	amqp "github.com/rabbitmq/amqp091-go"
)

type VideoWorker struct {
	ch             *amqp.Channel
	videoRepo      *video.VideoRepository
	socialRepo     *social.SocialRepository
	cache          *rediscache.Client
	queue          string
	bigVThreshold  int64 // 粉丝数超过此值视为大 V，跳过写扩散改用读扩散
}

func NewVideoWorker(ch *amqp.Channel, videoRepo *video.VideoRepository, socialRepo *social.SocialRepository, cache *rediscache.Client, queue string, bigVThreshold int64) *VideoWorker {
	if bigVThreshold <= 0 {
		bigVThreshold = 1000
	}
	return &VideoWorker{ch: ch, videoRepo: videoRepo, socialRepo: socialRepo, cache: cache, queue: queue, bigVThreshold: bigVThreshold}
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
	start := time.Now()
	err := w.process(ctx, d.Body)
	success := err == nil

	// 记录 MQ 消费指标
	action := "unknown"
	var evt rabbitmq.VideoEvent
	if json.Unmarshal(d.Body, &evt) == nil {
		action = evt.Action
	}
	metrics.RecordMqConsume(w.queue, action, success, time.Since(start))

	if err != nil {
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

	// score 统一用事件发生时间，保证同一视频在 inbox 和 outbox 里的 score 完全一致
	// 不能在 Worker 里 time.Now()：两条消息处理时间不同会导致 score 偏差，lister 合并时排序错乱
	score := evt.OccurredAt.UnixMilli()
	if score <= 0 {
		score = time.Now().UnixMilli()
	}

	switch evt.Action {
	case "publish_inbox":
		// 大 V 判断：先查粉丝数，超过阈值跳过写扩散
		// 大 V 的关注流由 lister 读时从 outbox 拉取，publish_outbox 事件会单独处理
		followerCount, err := w.socialRepo.CountFollowers(ctx, evt.AuthorID)
		if err != nil {
			return err
		}
		if followerCount >= w.bigVThreshold {
			log.Printf("video worker: author %d is big V (%d followers), skip inbox fanout", evt.AuthorID, followerCount)
			// 记录大 V 跳过写扩散
			metrics.BigVSkipCount.Inc()
			return nil
		}

		const batchSize = 500
		var lastRelationID uint
		totalFanout := 0
		for {
			followers, nextCursor, err := w.socialRepo.GetFollowersBatch(ctx, evt.AuthorID, lastRelationID, batchSize)
			if err != nil {
				metrics.InboxFanoutTotal.WithLabelValues("failed").Inc()
				return err
			}
			if len(followers) == 0 {
				break
			}

			totalFanout += len(followers)

			err = w.videoRepo.BatchInsertFollowFeedInbox(ctx, followers, int64(evt.VideoID), int64(evt.AuthorID), score)
			if err != nil {
				var mysqlErr *mysql.MySQLError
				if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
					// 唯一键冲突，说明已经插过了，幂等处理
					return nil
				}
				return err
			}

			// 构造这批粉丝的 inbox key，key 的格式定义在调用方而非 redis 层
			inboxKeys := make([]string, 0, len(followers))
			for _, f := range followers {
				inboxKeys = append(inboxKeys, fmt.Sprintf("follow:inbox:%d", f.ID))
			}
			if redisErr := w.cache.ZAddBatchInbox(ctx, inboxKeys, float64(score), strconv.FormatInt(int64(evt.VideoID), 10), 500, 7*24*time.Hour); redisErr != nil {
				log.Printf("video worker: redis inbox fanout failed: %v", redisErr)
				// Redis 写失败不影响主流程，DB 已写成功，读时可降级查 DB
			}

			if len(followers) < batchSize {
				break
			}
			lastRelationID = nextCursor
		}

		// 记录写扩散成功指标
		metrics.InboxFanoutTotal.WithLabelValues("success").Inc()
		metrics.InboxFanoutSize.Observe(float64(totalFanout))
		return nil

	case "publish_outbox":
		// 只有大 V 才写 outbox（读扩散）
		// 普通作者已通过 publish_inbox 写入粉丝收件箱，不再写 outbox，避免读时去重
		followerCount, err := w.socialRepo.CountFollowers(ctx, evt.AuthorID)
		if err != nil {
			return err
		}
		if followerCount < w.bigVThreshold {
			log.Printf("video worker: author %d is not big V (%d followers), skip outbox write", evt.AuthorID, followerCount)
			return nil
		}

		err = w.videoRepo.InsertFeedOutbox(ctx, int64(evt.AuthorID), int64(evt.VideoID), score)
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
