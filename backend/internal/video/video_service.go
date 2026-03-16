package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
)

type VideoService struct {
	repo         *VideoRepository
	cache        *rediscache.Client
	cacheTTL     time.Duration
	popularityMQ *rabbitmq.PopularityMQ
	videoMQ      *rabbitmq.VideoMQ
}

func NewVideoService(repo *VideoRepository, cache *rediscache.Client, popularityMQ *rabbitmq.PopularityMQ, videoMQ *rabbitmq.VideoMQ) *VideoService {
	return &VideoService{repo: repo, cache: cache, cacheTTL: 5 * time.Minute, popularityMQ: popularityMQ, videoMQ: videoMQ}
}

func (vs *VideoService) Publish(ctx context.Context, video *Video) error {
	if video == nil {
		return errors.New("video is nil")
	}

	video.Title = strings.TrimSpace(video.Title)
	video.PlayURL = strings.TrimSpace(video.PlayURL)
	video.CoverURL = strings.TrimSpace(video.CoverURL)

	if video.Title == "" {
		return errors.New("title is required")
	}
	if video.PlayURL == "" {
		return errors.New("play url is required")
	}
	if video.CoverURL == "" {
		return errors.New("cover url is required")
	}
	// 主链路首先保证数据落入源数据库
	if err := vs.repo.CreateVideo(ctx, video); err != nil {
		return err
	}

	if vs.videoMQ != nil {
		// 异步保证推模式下数据落入每个关注用户收件箱里面
		// MQ 失败时降级直接写作者发件箱（收件箱 fanout 依赖 Worker，此处仅保证发件箱可用）
		if err := vs.videoMQ.PublishInbox(ctx, video.ID, video.AuthorID); err != nil {
			log.Printf("video service: publish inbox mq failed (video=%d): %v", video.ID, err)
		}
		// 异步写进自己的发件箱；MQ 失败则降级直写 DB
		if err := vs.videoMQ.PublishOutbox(ctx, video.ID, video.AuthorID); err != nil {
			log.Printf("video service: publish outbox mq failed (video=%d): %v", video.ID, err)
			if dbErr := vs.repo.InsertFeedOutbox(ctx, int64(video.AuthorID), int64(video.ID), 0); dbErr != nil {
				log.Printf("video service: fallback insert outbox failed (video=%d): %v", video.ID, dbErr)
			}
		}
	}

	return nil
}

func (vs *VideoService) Delete(ctx context.Context, id uint, authorID uint) error {
	video, err := vs.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if video == nil {
		return errors.New("video not found")
	}
	if video.AuthorID != authorID {
		return errors.New("unauthorized")
	}
	if err := vs.repo.DeleteVideo(ctx, id); err != nil {
		return err
	}
	if vs.cache != nil {
		cacheKey := fmt.Sprintf("video:detail:id=%d", id)
		_ = vs.cache.Del(context.Background(), cacheKey)
	}
	return nil
}

func (vs *VideoService) ListByAuthorID(ctx context.Context, authorID uint, offset int) ([]Video, error) {
	videos, err := vs.repo.ListByAuthorID(ctx, int64(authorID), 20, offset)
	if err != nil {
		return nil, err
	}
	return videos, nil
}

// hotThreshold 访问次数超过此值才视为热点视频并写入缓存
const hotThreshold = 3

func (vs *VideoService) GetDetail(ctx context.Context, id uint) (*Video, error) {
	cacheKey := fmt.Sprintf("video:detail:id=%d", id)
	hitKey := fmt.Sprintf("video:detail:hits:id=%d", id)

	getCached := func() (*Video, bool) {
		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		b, err := vs.cache.GetBytes(opCtx, cacheKey)
		if err != nil {
			return nil, false
		}
		var cached Video
		if err := json.Unmarshal(b, &cached); err != nil {
			return nil, false
		}
		return &cached, true
	}

	setCached := func(video *Video) {
		b, err := json.Marshal(video)
		if err != nil {
			return
		}
		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		_ = vs.cache.SetBytes(opCtx, cacheKey, b, vs.cacheTTL)
	}

	if vs.cache != nil {
		// 热点视频直接从缓存返回
		if v, ok := getCached(); ok {
			return v, nil
		}

		// 缓存未命中：累加访问计数，判断是否到达热点阈值
		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		hits, _ := vs.cache.Incr(opCtx, hitKey)
		// 首次计数时设置 TTL，避免计数 key 永久占用内存
		if hits == 1 {
			_ = vs.cache.Expire(opCtx, hitKey, time.Hour)
		}
		cancel()

		// 达到热点阈值：做缓存击穿防护后写入缓存
		if hits >= hotThreshold {
			lockKey := "lock:" + cacheKey
			lockCtx, lockCancel := context.WithTimeout(ctx, 50*time.Millisecond)
			token, locked, lockErr := vs.cache.Lock(lockCtx, lockKey, 2*time.Second)
			lockCancel()

			if lockErr == nil && locked {
				defer func() { _ = vs.cache.Unlock(context.Background(), lockKey, token) }()
				// double-check：可能其他协程已经回填
				if v, ok := getCached(); ok {
					return v, nil
				}
				video, err := vs.repo.GetByID(ctx, id)
				if err != nil {
					return nil, err
				}
				setCached(video)
				return video, nil
			}

			// 没抢到锁：等待持锁协程回填缓存
			for i := 0; i < 5; i++ {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
				if v, ok := getCached(); ok {
					return v, nil
				}
			}
		}
	}

	// 冷门视频直接查 DB，不写缓存
	return vs.repo.GetByID(ctx, id)
}

func (vs *VideoService) UpdateLikesCount(ctx context.Context, id uint, likesCount int64) error {
	if err := vs.repo.UpdateLikesCount(ctx, id, likesCount); err != nil {
		return err
	}
	return nil
}

func (vs *VideoService) UpdatePopularity(ctx context.Context, id uint, change int64) error {
	if err := vs.repo.UpdatePopularity(ctx, id, change); err != nil {
		return err
	}

	if vs.popularityMQ != nil {
		if err := vs.popularityMQ.Update(ctx, id, change); err == nil {
			return nil
		}
	}

	if vs.cache != nil {
		// 1) 详情缓存：直接失效（最简单靠谱）
		_ = vs.cache.Del(context.Background(), fmt.Sprintf("video:detail:id=%d", id))

		// 2) 热榜：写到“时间窗ZSET”，不要用 detail key
		now := time.Now().UTC().Truncate(time.Minute)
		windowKey := "hot:video:1m:" + now.Format("200601021504")
		member := strconv.FormatUint(uint64(id), 10)

		opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		_ = vs.cache.ZincrBy(opCtx, windowKey, member, float64(change))
		_ = vs.cache.Expire(opCtx, windowKey, 2*time.Hour)
	}
	return nil
}
