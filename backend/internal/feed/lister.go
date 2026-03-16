package feed

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/video"
)

// FeedLister 每种 feed 类型实现此接口
type FeedLister interface {
	// BuildBuckets 从请求中还原或初始化游标状态
	BuildBuckets(ctx context.Context, req *FetchFeedsRequest) (BucketCollection, error)
	// FetchVideoList 拉取一页数据，并就地更新 bucket 游标
	FetchVideoList(ctx context.Context, viewerID uint, limit int, bucket BucketCollection) ([]*video.Video, error)
}

// =========== latestLister ===========

type latestLister struct {
	repo *FeedRepository
}

func (l *latestLister) BuildBuckets(_ context.Context, req *FetchFeedsRequest) (BucketCollection, error) {
	b := &BucketDetail{Type: Latest}
	if req.Bucket != nil {
		if bc, err := bucketFromString(*req.Bucket); err == nil {
			if restored := bc[Latest]; restored != nil {
				b = restored
			}
		}
	}
	return BucketCollection{Latest: b}, nil
}

func (l *latestLister) FetchVideoList(ctx context.Context, _ uint, limit int, bucket BucketCollection) ([]*video.Video, error) {
	b := bucket[Latest]
	var before time.Time
	if b.ScoreBefore > 0 {
		before = time.Unix(b.ScoreBefore, 0)
	}
	videos, err := l.repo.ListLatest(ctx, limit, before)
	if err != nil {
		return nil, err
	}
	if len(videos) > 0 {
		b.ScoreBefore = videos[len(videos)-1].CreateTime.Unix()
	}
	return videos, nil
}

// =========== followingsLister (读收件箱推模式) ===========

type followingsLister struct {
	repo       *FeedRepository
	socialRepo socialFollowingReader
	cache      *rediscache.Client
}

// socialFollowingReader 只暴露 followingsLister 需要的接口，避免依赖整个 SocialRepository
type socialFollowingReader interface {
	GetFollowingIDs(ctx context.Context, followerID uint) ([]uint, error)
}

func (l *followingsLister) BuildBuckets(_ context.Context, req *FetchFeedsRequest) (BucketCollection, error) {
	b := &BucketDetail{Type: Followings}
	if req.Bucket != nil {
		if bc, err := bucketFromString(*req.Bucket); err == nil {
			if restored := bc[Followings]; restored != nil {
				b = restored
			}
		}
	}
	return BucketCollection{Followings: b}, nil
}

func (l *followingsLister) FetchVideoList(ctx context.Context, viewerID uint, limit int, bucket BucketCollection) ([]*video.Video, error) {
	if viewerID == 0 {
		return nil, errors.New("followings feed requires login")
	}
	b := bucket[Followings]

	// --- inbox：普通作者推来的内容 ---
	// 优先 Redis，冷启动首页降级 MySQL inbox 表
	inboxVideos, inboxNextScore, err := l.fetchInbox(ctx, viewerID, b.ScoreBefore, limit)
	if err != nil {
		return nil, err
	}

	// --- outbox：所有关注对象的发件箱（覆盖大 V 读扩散） ---
	// 普通作者的视频可能同时出现在 inbox 和 outbox，合并时去重
	followingIDs, err := l.socialRepo.GetFollowingIDs(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	outboxVideos, outboxNextScore, err := l.repo.GetOutboxByAuthors(ctx, followingIDs, b.OutboxScoreBefore, limit)
	if err != nil {
		return nil, err
	}

	// --- 合并 + 去重 + 取前 limit 条 ---
	merged := mergeByCreateTime(inboxVideos, outboxVideos, limit)

	// 更新游标：以本页实际返回的最后一条 CreateTime 为准
	// inbox 和 outbox 各自只推进"被消费到"的那一侧
	if inboxNextScore > 0 {
		b.ScoreBefore = inboxNextScore
	}
	if outboxNextScore > 0 {
		b.OutboxScoreBefore = outboxNextScore
	}

	return merged, nil
}

// fetchInbox 优先 Redis inbox，首页 Redis 空时降级 MySQL inbox 表
func (l *followingsLister) fetchInbox(ctx context.Context, viewerID uint, scoreBefore int64, limit int) ([]*video.Video, int64, error) {
	isFirstPage := scoreBefore == 0

	if l.cache != nil {
		videos, nextScore, hit, err := l.tryRedisInbox(ctx, viewerID, scoreBefore, limit)
		if err == nil && (hit || !isFirstPage) {
			return videos, nextScore, nil
		}
	}

	videos, nextScore, err := l.repo.ListByFollowingInbox(ctx, viewerID, scoreBefore, limit)
	return videos, nextScore, err
}

// mergeByCreateTime 将两个按 CreateTime 降序排列的列表合并，取前 limit 条
// 写时分流保证 inbox（普通作者）和 outbox（大V）不重叠，无需去重
func mergeByCreateTime(a, b []*video.Video, limit int) []*video.Video {
	result := make([]*video.Video, 0, limit)
	i, j := 0, 0
	for len(result) < limit && (i < len(a) || j < len(b)) {
		var pick *video.Video
		switch {
		case i >= len(a):
			pick = b[j]; j++
		case j >= len(b):
			pick = a[i]; i++
		case a[i].CreateTime.After(b[j].CreateTime):
			pick = a[i]; i++
		default:
			pick = b[j]; j++
		}
		result = append(result, pick)
	}
	return result
}

// tryRedisInbox 从 Redis ZSET 读取当前用户的关注收件箱
// 返回 (videos, nextScore, hit, err)
//   - hit=false + err=nil 表示 Redis 中该用户收件箱为空（非错误）
//   - hit=true 时 nextScore 是本批最小 score，作为下一页游标
func (l *followingsLister) tryRedisInbox(ctx context.Context, viewerID uint, scoreBefore int64, limit int) ([]*video.Video, int64, bool, error) {
	key := fmt.Sprintf("follow:inbox:%d", viewerID)

	// 用 "(" 前缀表示开区间，避免下一页重复拿到上一页最后一条
	maxScore := "+inf"
	if scoreBefore > 0 {
		maxScore = "(" + strconv.FormatInt(scoreBefore, 10)
	}

	opCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()

	items, err := l.cache.ZRevRangeByScoreWithScores(opCtx, key, maxScore, "-inf", int64(limit))
	if err != nil {
		return nil, 0, false, err
	}
	if len(items) == 0 {
		return nil, 0, false, nil
	}

	// 解析视频 ID
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		u, parseErr := strconv.ParseUint(item.Member, 10, 64)
		if parseErr == nil && u > 0 {
			ids = append(ids, uint(u))
		}
	}

	// 批量从 DB 拿视频详情（Redis 只存 ID，不存完整数据）
	videos, err := l.repo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, 0, false, err
	}

	// 保持 Redis 返回的 score 降序，不能用 DB 返回顺序（DB IN 查询不保证顺序）
	ordered := reorderByIDs(videos, ids)

	// 本批最后一条的 score 作为下一页游标
	nextScore := items[len(items)-1].Score
	return ordered, nextScore, true, nil
}

// =========== likesLister ===========

type likesLister struct {
	repo *FeedRepository
}

func (l *likesLister) BuildBuckets(_ context.Context, req *FetchFeedsRequest) (BucketCollection, error) {
	b := &BucketDetail{Type: OrderByLikes}
	if req.Bucket != nil {
		if bc, err := bucketFromString(*req.Bucket); err == nil {
			if restored := bc[OrderByLikes]; restored != nil {
				b = restored
			}
		}
	}
	return BucketCollection{OrderByLikes: b}, nil
}

func (l *likesLister) FetchVideoList(ctx context.Context, _ uint, limit int, bucket BucketCollection) ([]*video.Video, error) {
	b := bucket[OrderByLikes]
	var cursor *LikesCountCursor
	if b.IDBefore > 0 {
		cursor = &LikesCountCursor{LikesCount: b.ScoreBefore, ID: b.IDBefore}
	}
	videos, err := l.repo.ListLikesCountWithCursor(ctx, limit, cursor)
	if err != nil {
		return nil, err
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		b.ScoreBefore = last.LikesCount
		b.IDBefore = last.ID
	}
	return videos, nil
}

// =========== popularityLister (Redis ZSET + DB fallback) ===========

type popularityLister struct {
	repo  *FeedRepository
	cache *rediscache.Client
}

func (l *popularityLister) BuildBuckets(_ context.Context, req *FetchFeedsRequest) (BucketCollection, error) {
	b := &BucketDetail{Type: OrderByPopularity}
	if req.Bucket != nil {
		if bc, err := bucketFromString(*req.Bucket); err == nil {
			if restored := bc[OrderByPopularity]; restored != nil {
				b = restored
			}
		}
	}
	return BucketCollection{OrderByPopularity: b}, nil
}

func (l *popularityLister) FetchVideoList(ctx context.Context, _ uint, limit int, bucket BucketCollection) ([]*video.Video, error) {
	b := bucket[OrderByPopularity]

	if l.cache != nil {
		asOf := time.Now().UTC().Truncate(time.Minute)
		if b.AsOf > 0 {
			asOf = time.Unix(b.AsOf, 0).UTC().Truncate(time.Minute)
		}

		const win = 60
		keys := make([]string, 0, win)
		for i := 0; i < win; i++ {
			keys = append(keys, "hot:video:1m:"+asOf.Add(-time.Duration(i)*time.Minute).Format("200601021504"))
		}

		dest := "hot:video:merge:1m:" + asOf.Format("200601021504")
		opCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		defer cancel()

		exists, _ := l.cache.Exists(opCtx, dest)
		if !exists {
			_ = l.cache.ZUnionStore(opCtx, dest, keys, "SUM")
			_ = l.cache.Expire(opCtx, dest, 2*time.Minute)
		}

		start := int64(b.Offset)
		stop := start + int64(limit) - 1
		members, err := l.cache.ZRevRange(opCtx, dest, start, stop)
		if err == nil && len(members) > 0 {
			ids := make([]uint, 0, len(members))
			for _, m := range members {
				u, parseErr := strconv.ParseUint(m, 10, 64)
				if parseErr == nil && u > 0 {
					ids = append(ids, uint(u))
				}
			}
			videos, dbErr := l.repo.GetByIDs(ctx, ids)
			if dbErr == nil {
				b.AsOf = asOf.Unix()
				b.Offset += len(videos)
				return reorderByIDs(videos, ids), nil
			}
		}

		// Redis 本页已无数据且非首页，直接返回空（首页 Redis 无数据时降级 DB）
		if err == nil && len(members) == 0 && b.Offset > 0 {
			return []*video.Video{}, nil
		}
	}

	// DB fallback
	var latestPopularity int64
	var timeBefore time.Time
	var idBefore uint
	if b.IDBefore > 0 {
		latestPopularity = b.ScoreBefore
		timeBefore = time.Unix(b.TimeBefore, 0)
		idBefore = b.IDBefore
	}
	videos, err := l.repo.ListByPopularity(ctx, limit, latestPopularity, timeBefore, idBefore)
	if err != nil {
		return nil, err
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		b.ScoreBefore = last.Popularity
		b.TimeBefore = last.CreateTime.Unix()
		b.IDBefore = last.ID
	}
	return videos, nil
}

// reorderByIDs 按照 Redis 返回的 ids 顺序重排 videos
func reorderByIDs(videos []*video.Video, ids []uint) []*video.Video {
	byID := make(map[uint]*video.Video, len(videos))
	for _, v := range videos {
		byID[v.ID] = v
	}
	result := make([]*video.Video, 0, len(ids))
	for _, id := range ids {
		if v := byID[id]; v != nil {
			result = append(result, v)
		}
	}
	return result
}
