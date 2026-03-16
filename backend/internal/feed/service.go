package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/video"
)

type FeedService struct {
	repo      *FeedRepository
	likeRepo  *video.LikeRepository
	cache     *rediscache.Client
	cacheTTL  time.Duration
	listerMap map[QueryType]FeedLister
}

func NewFeedService(repo *FeedRepository, likeRepo *video.LikeRepository, cache *rediscache.Client, socialRepo socialFollowingReader) *FeedService {
	svc := &FeedService{
		repo:     repo,
		likeRepo: likeRepo,
		cache:    cache,
		cacheTTL: 5 * time.Second,
	}
	svc.listerMap = map[QueryType]FeedLister{
		Latest:            &latestLister{repo: repo},
		Followings:        &followingsLister{repo: repo, socialRepo: socialRepo, cache: cache},
		OrderByLikes:      &likesLister{repo: repo},
		OrderByPopularity: &popularityLister{repo: repo, cache: cache},
	}
	return svc
}

// FetchFeeds 统一入口：根据 QType 分派到对应的 FeedLister
func (f *FeedService) FetchFeeds(ctx context.Context, req *FetchFeedsRequest, viewerID uint) (*FetchFeedsResponse, error) {
	lister := f.listerMap[req.QType]
	if lister == nil {
		return nil, fmt.Errorf("unsupported query type: %s", req.QType)
	}

	bucket, err := lister.BuildBuckets(ctx, req)
	if err != nil {
		return nil, err
	}

	videos, err := lister.FetchVideoList(ctx, viewerID, req.Limit, bucket)
	if err != nil {
		return nil, err
	}

	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerID)
	if err != nil {
		return nil, err
	}

	bucketStr, err := bucket.toString()
	if err != nil {
		return nil, err
	}

	return &FetchFeedsResponse{
		VideoList: feedVideos,
		HasMore:   len(videos) == req.Limit,
		Bucket:    &bucketStr,
	}, nil
}

// 查询最新视频（旧接口，保留向后兼容）
func (f *FeedService) ListLatest(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListLatestResponse, error) {
	doListLatestFromDB := func() (ListLatestResponse, error) {
		videos, err := f.repo.ListLatest(ctx, limit, latestBefore)
		if err != nil {
			return ListLatestResponse{}, err
		}
		var nextTime int64
		if len(videos) > 0 {
			nextTime = videos[len(videos)-1].CreateTime.Unix()
		}
		feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
		if err != nil {
			return ListLatestResponse{}, err
		}
		return ListLatestResponse{
			VideoList: feedVideos,
			NextTime:  nextTime,
			HasMore:   len(videos) == limit,
		}, nil
	}

	var cacheKey string
	if viewerAccountID == 0 && f.cache != nil {
		before := int64(0)
		if !latestBefore.IsZero() {
			before = latestBefore.Unix()
		}
		cacheKey = fmt.Sprintf("feed:listLatest:limit=%d:before=%d", limit, before)

		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		b, err := f.cache.GetBytes(cacheCtx, cacheKey)
		if err == nil {
			var cached ListLatestResponse
			if err := json.Unmarshal(b, &cached); err == nil {
				return cached, nil
			}
		} else if rediscache.IsMiss(err) {
			lockKey := "lock:" + cacheKey
			token, locked, _ := f.cache.Lock(cacheCtx, lockKey, 500*time.Millisecond)
			if locked {
				defer func() { _ = f.cache.Unlock(context.Background(), lockKey, token) }()
				if b, err := f.cache.GetBytes(cacheCtx, cacheKey); err == nil {
					var cached ListLatestResponse
					if err := json.Unmarshal(b, &cached); err == nil {
						return cached, nil
					}
				} else {
					resp, err := doListLatestFromDB()
					if err != nil {
						return ListLatestResponse{}, err
					}
					if b, err := json.Marshal(resp); err == nil {
						_ = f.cache.SetBytes(cacheCtx, cacheKey, b, f.cacheTTL)
					}
					return resp, nil
				}
			} else {
				for i := 0; i < 5; i++ {
					time.Sleep(20 * time.Millisecond)
					if b, err := f.cache.GetBytes(cacheCtx, cacheKey); err == nil {
						var cached ListLatestResponse
						if err := json.Unmarshal(b, &cached); err == nil {
							return cached, nil
						}
					}
				}
			}
		}
	}

	resp, err := doListLatestFromDB()
	if err != nil {
		return ListLatestResponse{}, err
	}
	if cacheKey != "" {
		if b, err := json.Marshal(resp); err == nil {
			cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_ = f.cache.SetBytes(cacheCtx, cacheKey, b, f.cacheTTL)
		}
	}
	return resp, nil
}

// 按照点赞数查询视频（旧接口，保留向后兼容）
func (f *FeedService) ListLikesCount(ctx context.Context, limit int, cursor *LikesCountCursor, viewerAccountID uint) (ListLikesCountResponse, error) {
	videos, err := f.repo.ListLikesCountWithCursor(ctx, limit, cursor)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListLikesCountResponse{}, err
	}
	resp := ListLikesCountResponse{
		VideoList: feedVideos,
		HasMore:   len(videos) == limit,
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextLikesCountBefore := last.LikesCount
		nextIDBefore := last.ID
		resp.NextLikesCountBefore = &nextLikesCountBefore
		resp.NextIDBefore = &nextIDBefore
	}
	return resp, nil
}

// 按照关注列表查询视频（旧接口，保留向后兼容）
func (f *FeedService) ListByFollowing(ctx context.Context, limit int, latestBefore time.Time, viewerAccountID uint) (ListByFollowingResponse, error) {
	doListByFollowingFromDB := func() (ListByFollowingResponse, error) {
		videos, err := f.repo.ListByFollowing(ctx, limit, viewerAccountID, latestBefore)
		if err != nil {
			return ListByFollowingResponse{}, err
		}
		var nextTime int64
		if len(videos) > 0 {
			nextTime = videos[len(videos)-1].CreateTime.Unix()
		}
		feedVideos, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
		if err != nil {
			return ListByFollowingResponse{}, err
		}
		return ListByFollowingResponse{
			VideoList: feedVideos,
			NextTime:  nextTime,
			HasMore:   len(videos) == limit,
		}, nil
	}

	var cacheKey string
	if viewerAccountID != 0 && f.cache != nil {
		before := int64(0)
		if !latestBefore.IsZero() {
			before = latestBefore.Unix()
		}
		cacheKey = fmt.Sprintf("feed:listByFollowing:limit=%d:accountID=%d:before=%d", limit, viewerAccountID, before)
		cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		b, err := f.cache.GetBytes(cacheCtx, cacheKey)
		if err == nil {
			var cached ListByFollowingResponse
			if err := json.Unmarshal(b, &cached); err == nil {
				return cached, nil
			}
		} else if rediscache.IsMiss(err) {
			lockKey := "lock:" + cacheKey
			token, locked, _ := f.cache.Lock(cacheCtx, lockKey, 500*time.Millisecond)
			if locked {
				defer func() { _ = f.cache.Unlock(context.Background(), lockKey, token) }()
				if b, err := f.cache.GetBytes(cacheCtx, cacheKey); err == nil {
					var cached ListByFollowingResponse
					if err := json.Unmarshal(b, &cached); err == nil {
						return cached, nil
					}
				} else {
					resp, err := doListByFollowingFromDB()
					if err != nil {
						return ListByFollowingResponse{}, err
					}
					if b, err := json.Marshal(resp); err == nil {
						_ = f.cache.SetBytes(cacheCtx, cacheKey, b, f.cacheTTL)
					}
					return resp, nil
				}
			} else {
				for i := 0; i < 5; i++ {
					time.Sleep(20 * time.Millisecond)
					if b, err := f.cache.GetBytes(cacheCtx, cacheKey); err == nil {
						var cached ListByFollowingResponse
						if err := json.Unmarshal(b, &cached); err == nil {
							return cached, nil
						}
					}
				}
			}
		}
	}

	resp, err := doListByFollowingFromDB()
	if err != nil {
		return ListByFollowingResponse{}, err
	}
	if cacheKey != "" {
		if b, err := json.Marshal(resp); err == nil {
			cacheCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_ = f.cache.SetBytes(cacheCtx, cacheKey, b, f.cacheTTL)
		}
	}
	return resp, nil
}

// 按照热度查询视频（旧接口，保留向后兼容）
func (f *FeedService) ListByPopularity(ctx context.Context, limit int, reqAsOf int64, offset int, viewerAccountID uint, latestPopularity int64, latestBefore time.Time, latestIDBefore uint) (ListByPopularityResponse, error) {
	if f.cache != nil {
		asOf := time.Now().UTC().Truncate(time.Minute)
		if reqAsOf > 0 {
			asOf = time.Unix(reqAsOf, 0).UTC().Truncate(time.Minute)
		}

		const win = 60
		keys := make([]string, 0, win)
		for i := 0; i < win; i++ {
			keys = append(keys, "hot:video:1m:"+asOf.Add(-time.Duration(i)*time.Minute).Format("200601021504"))
		}

		dest := "hot:video:merge:1m:" + asOf.Format("200601021504")
		opCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		defer cancel()

		exists, _ := f.cache.Exists(opCtx, dest)
		if !exists {
			_ = f.cache.ZUnionStore(opCtx, dest, keys, "SUM")
			_ = f.cache.Expire(opCtx, dest, 2*time.Minute)
		}

		start := int64(offset)
		stop := start + int64(limit) - 1
		members, err := f.cache.ZRevRange(opCtx, dest, start, stop)
		if err == nil && len(members) == 0 && offset > 0 {
			return ListByPopularityResponse{
				VideoList:  []FeedVideoItem{},
				AsOf:       asOf.Unix(),
				NextOffset: offset,
				HasMore:    false,
			}, nil
		}
		if err == nil && len(members) > 0 {
			ids := make([]uint, 0, len(members))
			for _, m := range members {
				u, err := strconv.ParseUint(m, 10, 64)
				if err == nil && u > 0 {
					ids = append(ids, uint(u))
				}
			}
			videos, err := f.repo.GetByIDs(ctx, ids)
			if err == nil {
				ordered := reorderByIDs(videos, ids)
				items, err := f.buildFeedVideos(ctx, ordered, viewerAccountID)
				if err != nil {
					return ListByPopularityResponse{}, err
				}
				resp := ListByPopularityResponse{
					VideoList:  items,
					AsOf:       asOf.Unix(),
					NextOffset: offset + len(items),
					HasMore:    len(items) == limit,
				}
				if len(ordered) > 0 {
					last := ordered[len(ordered)-1]
					nextPopularity := last.Popularity
					nextBefore := last.CreateTime
					nextID := last.ID
					resp.NextLatestPopularity = &nextPopularity
					resp.NextLatestBefore = &nextBefore
					resp.NextLatestIDBefore = &nextID
				}
				return resp, nil
			}
		}
	}

	videos, err := f.repo.ListByPopularity(ctx, limit, latestPopularity, latestBefore, latestIDBefore)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	items, err := f.buildFeedVideos(ctx, videos, viewerAccountID)
	if err != nil {
		return ListByPopularityResponse{}, err
	}
	resp := ListByPopularityResponse{
		VideoList: items,
		HasMore:   len(items) == limit,
	}
	if len(videos) > 0 {
		last := videos[len(videos)-1]
		nextPopularity := last.Popularity
		nextBefore := last.CreateTime
		nextID := last.ID
		resp.NextLatestPopularity = &nextPopularity
		resp.NextLatestBefore = &nextBefore
		resp.NextLatestIDBefore = &nextID
	}
	return resp, nil
}

func (f *FeedService) buildFeedVideos(ctx context.Context, videos []*video.Video, viewerAccountID uint) ([]FeedVideoItem, error) {
	feedVideos := make([]FeedVideoItem, 0, len(videos))
	videoIDs := make([]uint, len(videos))
	for i, v := range videos {
		videoIDs[i] = v.ID
	}
	likedMap, err := f.likeRepo.BatchGetLiked(ctx, videoIDs, viewerAccountID)
	if err != nil {
		return nil, err
	}
	for _, v := range videos {
		feedVideos = append(feedVideos, FeedVideoItem{
			ID:          v.ID,
			Author:      FeedAuthor{ID: v.AuthorID, Username: v.Username},
			Title:       v.Title,
			Description: v.Description,
			PlayURL:     v.PlayURL,
			CoverURL:    v.CoverURL,
			CreateTime:  v.CreateTime.Unix(),
			LikesCount:  v.LikesCount,
			IsLiked:     likedMap[v.ID],
		})
	}
	return feedVideos, nil
}
