package feed

import (
	"context"
	"feedsystem_video_go/internal/social"
	"feedsystem_video_go/internal/video"
	"time"

	"gorm.io/gorm"
)

type FeedRepository struct {
	db *gorm.DB
}

func NewFeedRepository(db *gorm.DB) *FeedRepository {
	return &FeedRepository{db: db}
}

func (repo *FeedRepository) ListLatest(ctx context.Context, limit int, latestBefore time.Time) ([]*video.Video, error) {
	var videos []*video.Video
	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("create_time DESC")
	if !latestBefore.IsZero() {
		query = query.Where("create_time < ?", latestBefore)
	}
	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

func (repo *FeedRepository) ListLikesCountWithCursor(ctx context.Context, limit int, cursor *LikesCountCursor) ([]*video.Video, error) {
	var videos []*video.Video
	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("likes_count DESC, id DESC")

	if cursor != nil {
		query = query.Where(
			"(likes_count < ?) OR (likes_count = ? AND id < ?)",
			cursor.LikesCount,
			cursor.LikesCount, cursor.ID,
		)
	}

	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

func (repo *FeedRepository) ListByFollowing(ctx context.Context, limit int, viewerAccountID uint, latestBefore time.Time) ([]*video.Video, error) {
	var videos []*video.Video
	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("create_time DESC")
	if viewerAccountID > 0 {
		followingSubQuery := repo.db.WithContext(ctx).
			Model(&social.Social{}).
			Select("vlogger_id").
			Where("follower_id = ?", viewerAccountID)
		query = query.Where("author_id IN (?)", followingSubQuery)
	}
	if !latestBefore.IsZero() {
		query = query.Where("create_time < ?", latestBefore)
	}
	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

func (repo *FeedRepository) ListByPopularity(ctx context.Context, limit int, popularityBefore int64, timeBefore time.Time, idBefore uint) ([]*video.Video, error) {
	var videos []*video.Video
	query := repo.db.WithContext(ctx).Model(&video.Video{}).
		Order("popularity DESC, create_time DESC, id DESC")

	// 只有当游标完整提供时才加过滤（popularity 允许为 0）
	if !timeBefore.IsZero() && idBefore > 0 {
		query = query.Where(
			"(popularity < ?) OR (popularity = ? AND create_time < ?) OR (popularity = ? AND create_time = ? AND id < ?)",
			popularityBefore,
			popularityBefore, timeBefore,
			popularityBefore, timeBefore, idBefore,
		)
	}

	if err := query.Limit(limit).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// ListByFollowingInbox 从推送收件箱读取关注流
// 返回按 inbox score 排序的视频列表，以及下一页游标 nextScore
func (repo *FeedRepository) ListByFollowingInbox(ctx context.Context, userID uint, maxScore int64, limit int) ([]*video.Video, int64, error) {
	var items []video.FollowFeedInbox
	q := repo.db.WithContext(ctx).
		Where("user_id = ? AND is_deleted = 0", userID)
	if maxScore > 0 {
		q = q.Where("score < ?", maxScore)
	}
	if err := q.Order("score DESC, id DESC").Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	if len(items) == 0 {
		return []*video.Video{}, 0, nil
	}

	postIDs := make([]uint, 0, len(items))
	for _, item := range items {
		postIDs = append(postIDs, uint(item.PostID))
	}

	var rawVideos []*video.Video
	if err := repo.db.WithContext(ctx).Where("id IN ?", postIDs).Find(&rawVideos).Error; err != nil {
		return nil, 0, err
	}

	videoMap := make(map[uint]*video.Video, len(rawVideos))
	for _, v := range rawVideos {
		videoMap[v.ID] = v
	}

	// 按 inbox score 顺序重排，保持时间线一致性
	result := make([]*video.Video, 0, len(items))
	for _, item := range items {
		if v, ok := videoMap[uint(item.PostID)]; ok {
			result = append(result, v)
		}
	}

	nextScore := items[len(items)-1].Score
	return result, nextScore, nil
}

// GetOutboxByAuthors 拉取指定作者列表的发件箱内容，按 score 降序，支持游标翻页
// 用于关注流读扩散：大 V 不 fanout inbox，由此方法在读时按需拉取
// scoreBefore=0 表示首页；>0 表示拉 score 严格小于该值的下一页
func (repo *FeedRepository) GetOutboxByAuthors(ctx context.Context, authorIDs []uint, scoreBefore int64, limit int) ([]*video.Video, int64, error) {
	if len(authorIDs) == 0 {
		return nil, 0, nil
	}

	var outboxes []video.FeedOutbox
	q := repo.db.WithContext(ctx).
		Where("author_id IN ?", authorIDs).
		Order("score DESC, id DESC").
		Limit(limit)
	if scoreBefore > 0 {
		q = q.Where("score < ?", scoreBefore)
	}
	if err := q.Find(&outboxes).Error; err != nil {
		return nil, 0, err
	}
	if len(outboxes) == 0 {
		return nil, 0, nil
	}

	postIDs := make([]uint, 0, len(outboxes))
	for _, item := range outboxes {
		postIDs = append(postIDs, uint(item.PostID))
	}

	var rawVideos []*video.Video
	if err := repo.db.WithContext(ctx).Where("id IN ?", postIDs).Find(&rawVideos).Error; err != nil {
		return nil, 0, err
	}

	videoMap := make(map[uint]*video.Video, len(rawVideos))
	for _, v := range rawVideos {
		videoMap[v.ID] = v
	}

	// 按 outbox score 顺序重排，保持时间线一致
	result := make([]*video.Video, 0, len(outboxes))
	for _, item := range outboxes {
		if v, ok := videoMap[uint(item.PostID)]; ok {
			result = append(result, v)
		}
	}

	nextScore := outboxes[len(outboxes)-1].Score
	return result, nextScore, nil
}

func (repo *FeedRepository) GetByIDs(ctx context.Context, ids []uint) ([]*video.Video, error) {
	var videos []*video.Video
	if len(ids) == 0 {
		return videos, nil
	}
	if err := repo.db.WithContext(ctx).Model(&video.Video{}).
		Where("id IN ?", ids).Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}
