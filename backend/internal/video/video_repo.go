package video

import (
	"context"
	"errors"
	"feedsystem_video_go/internal/account"
	"time"

	"gorm.io/gorm"
)

type VideoRepository struct {
	db *gorm.DB
}

func NewVideoRepository(db *gorm.DB) *VideoRepository {
	return &VideoRepository{db: db}
}

func (vr *VideoRepository) CreateVideo(ctx context.Context, video *Video) error {
	if err := vr.db.WithContext(ctx).Create(video).Error; err != nil {
		return err
	}
	return nil
}

func (vr *VideoRepository) DeleteVideo(ctx context.Context, id uint) error {
	if err := vr.db.WithContext(ctx).Delete(&Video{}, id).Error; err != nil {
		return err
	}
	return nil
}

// ListByAuthorID 这里修改为两次查询，后续会进一步修改为一次查询
func (vr *VideoRepository) ListByAuthorID(ctx context.Context, authorID int64, limit, offset int) ([]Video, error) {
	if authorID == 0 {
		return nil, errors.New("authorID is required")
	}
	if limit <= 0 {
		limit = 20
	}

	var outboxes []FeedOutbox
	if err := vr.db.WithContext(ctx).
		Where("author_id = ?", authorID).
		Order("score DESC, id DESC").
		Limit(limit).
		Offset(offset).
		Find(&outboxes).Error; err != nil {
		return nil, err
	}

	if len(outboxes) == 0 {
		// outbox 无数据时降级直接查 video 表，保证个人主页始终有内容
		var videos []Video
		if err := vr.db.WithContext(ctx).
			Where("author_id = ?", authorID).
			Order("create_time DESC, id DESC").
			Limit(limit).
			Offset(offset).
			Find(&videos).Error; err != nil {
			return nil, err
		}
		return videos, nil
	}

	postIDs := make([]int64, 0, len(outboxes))
	for _, item := range outboxes {
		postIDs = append(postIDs, item.PostID)
	}

	var videos []Video
	if err := vr.db.WithContext(ctx).
		Where("id IN ?", postIDs).
		Find(&videos).Error; err != nil {
		return nil, err
	}

	videoMap := make(map[int64]Video, len(videos))
	for _, v := range videos {
		videoMap[int64(v.ID)] = v
	}

	result := make([]Video, 0, len(outboxes))
	for _, item := range outboxes {
		if v, ok := videoMap[item.PostID]; ok {
			result = append(result, v)
		}
	}

	return result, nil
}

func (vr *VideoRepository) GetByID(ctx context.Context, id uint) (*Video, error) {
	var video Video
	if err := vr.db.WithContext(ctx).First(&video, id).Error; err != nil {
		return (*Video)(nil), err
	}
	return &video, nil
}

func (vr *VideoRepository) UpdateLikesCount(ctx context.Context, id uint, likesCount int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		Update("likes_count", likesCount).Error; err != nil {
		return err
	}
	return nil
}

func (vr *VideoRepository) IsExist(ctx context.Context, id uint) (bool, error) {
	var video Video
	if err := vr.db.WithContext(ctx).First(&video, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (vr *VideoRepository) UpdatePopularity(ctx context.Context, id uint, change int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		Update("popularity", gorm.Expr("popularity + ?", change)).Error; err != nil {
		return err
	}
	return nil
}

func (vr *VideoRepository) ChangeLikesCount(ctx context.Context, id uint, change int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		UpdateColumn("likes_count", gorm.Expr("GREATEST(likes_count + ?, 0)", change)).Error; err != nil {
		return err
	}
	return nil
}

func (vr *VideoRepository) ChangePopularity(ctx context.Context, id uint, change int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		UpdateColumn("popularity", gorm.Expr("GREATEST(popularity + ?, 0)", change)).Error; err != nil {
		return err
	}
	return nil
}


func (vr *VideoRepository) BatchInsertFollowFeedInbox(ctx context.Context, followers []*account.Account, postID int64, authorID int64, score int64) error {
	if len(followers) == 0 {
		return nil
	}
	if postID == 0 || authorID == 0 {
		return errors.New("postID and authorID are required")
	}

	now := time.Now()
	items := make([]FollowFeedInbox, 0, len(followers))

	for _, follower := range followers {
		if follower.ID == 0 {
			continue
		}
		items = append(items, FollowFeedInbox{
			UserID:    int64(follower.ID),
			PostID:    postID,
			AuthorID:  authorID,
			Score:     score,
			IsRead:    0,
			IsDeleted: 0,
			CreatedAt: now,
		})
	}

	if len(items) == 0 {
		return nil
	}

	return vr.db.WithContext(ctx).CreateInBatches(items, 500).Error
}

func (vr *VideoRepository) InsertFeedOutbox(ctx context.Context, authorID int64, postID int64, score int64) error {
	if authorID == 0 || postID == 0 {
		return errors.New("authorID and postID are required")
	}
	if score == 0 {
		score = time.Now().UnixMilli()
	}

	item := FeedOutbox{
		AuthorID:  authorID,
		PostID:    postID,
		Score:     score,
		CreatedAt: time.Now(),
	}

	return vr.db.WithContext(ctx).Create(&item).Error
}

// UpdateVideoMeta 创作者确认 AI 建议后，将优化后的标题和标签写回视频记录
func (vr *VideoRepository) UpdateVideoMeta(ctx context.Context, id uint, title, tags string) error {
	if id == 0 {
		return errors.New("video id is required")
	}
	return vr.db.WithContext(ctx).
		Model(&Video{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"title": title,
			"tags":  tags,
		}).Error
}
