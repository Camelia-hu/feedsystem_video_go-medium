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

	"feedsystem_video_go/internal/middleware/localcache"
	"feedsystem_video_go/internal/middleware/metrics"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
)

type VideoService struct {
	repo             *VideoRepository
	suggestionRepo   *AISuggestionRepository
	l1               *localcache.Cache  // L1：进程内 LRU，TTL=1s
	cache            *rediscache.Client // L2：Redis，TTL=5min
	cacheTTL         time.Duration
	popularityMQ     *rabbitmq.PopularityMQ
	videoMQ          *rabbitmq.VideoMQ
	orchestrationMQ  *rabbitmq.OrchestrationMQ // Agentic 发布工作流
}

func NewVideoService(
	repo *VideoRepository,
	suggestionRepo *AISuggestionRepository,
	l1 *localcache.Cache,
	cache *rediscache.Client,
	popularityMQ *rabbitmq.PopularityMQ,
	videoMQ *rabbitmq.VideoMQ,
	orchestrationMQ *rabbitmq.OrchestrationMQ,
) *VideoService {
	return &VideoService{
		repo:            repo,
		suggestionRepo:  suggestionRepo,
		l1:              l1,
		cache:           cache,
		cacheTTL:        5 * time.Minute,
		popularityMQ:    popularityMQ,
		videoMQ:         videoMQ,
		orchestrationMQ: orchestrationMQ,
	}
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

	// 记录视频发布指标
	metrics.VideoPublishTotal.Inc()

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

	// 异步触发 Agentic 发布工作流：LLM 分析内容，生成标题/标签建议
	// 不阻塞主链路，MQ 失败仅记录日志
	if vs.orchestrationMQ != nil {
		if err := vs.orchestrationMQ.PublishAnalyze(ctx, video.ID, video.AuthorID, video.Title, video.Description); err != nil {
			log.Printf("video service: orchestration mq failed (video=%d): %v", video.ID, err)
		}
	}

	return nil
}

// GetAISuggestion 查询视频的 AI 内容建议（创作者 Human-in-the-loop 查看）
func (vs *VideoService) GetAISuggestion(ctx context.Context, videoID uint) (*VideoAISuggestion, error) {
	return vs.suggestionRepo.GetByVideoID(ctx, videoID)
}

// ConfirmAISuggestion 创作者确认 AI 建议，将优化后的标题/标签写回视频记录
func (vs *VideoService) ConfirmAISuggestion(ctx context.Context, videoID uint) (*VideoAISuggestion, error) {
	suggestion, err := vs.suggestionRepo.Confirm(ctx, videoID)
	if err != nil {
		return nil, err
	}

	// 将建议的标题和标签写回视频
	if suggestion.SuggestedTitle != "" {
		if updateErr := vs.repo.UpdateVideoMeta(ctx, videoID, suggestion.SuggestedTitle, suggestion.SuggestedTags); updateErr != nil {
			log.Printf("video service: update video meta failed (video=%d): %v", videoID, updateErr)
			return nil, updateErr
		}
		// 使 L1/L2 缓存失效，确保下次读取到最新数据
		vs.invalidateCache(ctx, videoID)
	}

	return suggestion, nil
}

// RejectAISuggestion 创作者拒绝 AI 建议
func (vs *VideoService) RejectAISuggestion(ctx context.Context, videoID uint) error {
	return vs.suggestionRepo.Reject(ctx, videoID)
}

// invalidateCache 使视频详情缓存失效（L1 + L2）
func (vs *VideoService) invalidateCache(ctx context.Context, videoID uint) {
	cacheKey := fmt.Sprintf("video:detail:id=%d", videoID)
	if vs.l1 != nil {
		vs.l1.Del(cacheKey)
	}
	if vs.cache != nil {
		_ = vs.cache.Del(ctx, cacheKey)
	}
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

	// 记录视频删除指标
	metrics.VideoDeleteTotal.Inc()

	cacheKey := fmt.Sprintf("video:detail:id=%d", id)
	vs.l1.Del(cacheKey)
	if vs.cache != nil {
		_ = vs.cache.Del(context.Background(), cacheKey)
		// 同步清除热榜 ZSET，避免已删除视频继续出现在热榜中
		member := strconv.FormatUint(uint64(id), 10)
		_ = vs.cache.ZRem(context.Background(), HotDecayKey, member)
	}
	return nil
}

// AdminDelete 管理员删除视频（不检查作者权限）
func (vs *VideoService) AdminDelete(ctx context.Context, id uint) error {
	video, err := vs.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if video == nil {
		return errors.New("video not found")
	}
	if err := vs.repo.DeleteVideo(ctx, id); err != nil {
		return err
	}

	// 记录视频删除指标
	metrics.VideoDeleteTotal.Inc()

	cacheKey := fmt.Sprintf("video:detail:id=%d", id)
	vs.l1.Del(cacheKey)
	if vs.cache != nil {
		_ = vs.cache.Del(context.Background(), cacheKey)
		// 同步清除热榜 ZSET，避免已删除视频继续出现在热榜中
		member := strconv.FormatUint(uint64(id), 10)
		_ = vs.cache.ZRem(context.Background(), HotDecayKey, member)
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

	// getCached 按 L1 → L2 顺序读缓存
	getCached := func() (*Video, bool) {
		start := time.Now()
		// L1：进程内 LRU，命中则直接返回,省去 Redis 网络往返
		if b, ok := vs.l1.Get(cacheKey); ok {
			var v Video
			if json.Unmarshal(b, &v) == nil {
				metrics.RecordCacheOperation("L1", "video:detail", true, time.Since(start))
				return &v, true
			}
		}
		metrics.RecordCacheOperation("L1", "video:detail", false, time.Since(start))

		// L2：Redis
		if vs.cache != nil {
			start = time.Now()
			opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			b, err := vs.cache.GetBytes(opCtx, cacheKey)
			if err == nil {
				var v Video
				if json.Unmarshal(b, &v) == nil {
					// 回填 L1，下次请求走进程内缓存
					vs.l1.Set(cacheKey, b, localcache.L1TTL)
					metrics.RecordCacheOperation("L2", "video:detail", true, time.Since(start))
					return &v, true
				}
			}
			metrics.RecordCacheOperation("L2", "video:detail", false, time.Since(start))
		}
		return nil, false
	}

	// setCached 同时写 L1（1s）和 L2（5min）
	setCached := func(video *Video) {
		b, err := json.Marshal(video)
		if err != nil {
			return
		}
		vs.l1.Set(cacheKey, b, localcache.L1TTL)
		if vs.cache != nil {
			opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_ = vs.cache.SetBytes(opCtx, cacheKey, b, vs.cacheTTL)
		}
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

	// 失效缓存，确保下次读取最新数据
	cacheKey := fmt.Sprintf("video:detail:id=%d", id)
	vs.l1.Del(cacheKey)
	if vs.cache != nil {
		_ = vs.cache.Del(context.Background(), cacheKey)
	}

	return nil
}

func (vs *VideoService) UpdatePopularity(ctx context.Context, id uint, change int64) error {
	if err := vs.repo.UpdatePopularity(ctx, id, change); err != nil {
		return err
	}

	// 记录热度更新指标
	source := "unknown"
	if change > 0 {
		source = "increase"
	} else if change < 0 {
		source = "decrease"
	}
	metrics.PopularityUpdateTotal.WithLabelValues(source).Inc()

	if vs.popularityMQ != nil {
		if err := vs.popularityMQ.Update(ctx, id, change); err == nil {
			return nil
		}
	}

	// MQ 不可用时直接写缓存兜底
	if vs.cache != nil {
		UpdatePopularityCache(ctx, vs.cache, id, change)
	}
	return nil
}
