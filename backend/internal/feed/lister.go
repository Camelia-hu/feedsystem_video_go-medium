package feed

import (
	"context"
	"errors"
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
	repo *FeedRepository
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
	videos, nextScore, err := l.repo.ListByFollowingInbox(ctx, viewerID, b.ScoreBefore, limit)
	if err != nil {
		return nil, err
	}
	b.ScoreBefore = nextScore
	return videos, nil
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
