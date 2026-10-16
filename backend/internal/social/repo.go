package social

import (
	"context"
	"feedsystem_video_go/internal/account"

	"gorm.io/gorm"
)

type SocialRepository struct {
	db *gorm.DB
}

func NewSocialRepository(db *gorm.DB) *SocialRepository {
	return &SocialRepository{db: db}
}

func (r *SocialRepository) Follow(ctx context.Context, social *Social) error {
	return r.db.WithContext(ctx).Create(social).Error
}

func (r *SocialRepository) Unfollow(ctx context.Context, social *Social) error {
	return r.db.WithContext(ctx).
		Where("follower_id = ? AND vlogger_id = ?", social.FollowerID, social.VloggerID).
		Delete(&Social{}).Error
}

func (r *SocialRepository) GetAllFollowers(ctx context.Context, VloggerID uint) ([]*account.Account, error) {
	var relations []Social
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("vlogger_id = ?", VloggerID).
		Find(&relations).Error; err != nil {
		return nil, err
	}

	followerIDs := make([]uint, 0, len(relations))
	for _, rel := range relations {
		followerIDs = append(followerIDs, rel.FollowerID)
	}
	if len(followerIDs) == 0 {
		return []*account.Account{}, nil
	}

	var followers []*account.Account
	if err := r.db.WithContext(ctx).
		Model(&account.Account{}).
		Where("id IN ?", followerIDs).
		Find(&followers).Error; err != nil {
		return nil, err
	}
	return followers, nil
}

// CountFollowers 返回指定作者的粉丝总数，用于大 V 阈值判断
// 只做 COUNT(*)，不拉 Account 记录，避免大粉丝量时的无效开销
func (r *SocialRepository) CountFollowers(ctx context.Context, vloggerID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Social{}).
		Where("vlogger_id = ?", vloggerID).
		Count(&count).Error
	return count, err
}

// GetFollowersBatch 游标分页拉取粉丝
// lastRelationID=0 表示从头开始；返回本批粉丝账号及下一页游标（social 表的最后一条 id）
func (r *SocialRepository) GetFollowersBatch(ctx context.Context, vloggerID uint, lastRelationID uint, limit int) ([]*account.Account, uint, error) {
	var relations []Social
	q := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("vlogger_id = ?", vloggerID).
		Order("id ASC").
		Limit(limit)
	if lastRelationID > 0 {
		q = q.Where("id > ?", lastRelationID)
	}
	if err := q.Find(&relations).Error; err != nil {
		return nil, 0, err
	}
	if len(relations) == 0 {
		return []*account.Account{}, 0, nil
	}

	followerIDs := make([]uint, 0, len(relations))
	for _, rel := range relations {
		followerIDs = append(followerIDs, rel.FollowerID)
	}

	var followers []*account.Account
	if err := r.db.WithContext(ctx).
		Model(&account.Account{}).
		Where("id IN ?", followerIDs).
		Find(&followers).Error; err != nil {
		return nil, 0, err
	}

	nextCursor := relations[len(relations)-1].ID
	return followers, nextCursor, nil
}

// GetFollowingIDs 返回用户关注的所有作者 ID，不拉 Account 对象
// 供 feed 层构造 outbox 查询用，避免不必要的 JOIN
func (r *SocialRepository) GetFollowingIDs(ctx context.Context, followerID uint) ([]uint, error) {
	var relations []Social
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Select("vlogger_id").
		Where("follower_id = ?", followerID).
		Find(&relations).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(relations))
	for _, rel := range relations {
		ids = append(ids, rel.VloggerID)
	}
	return ids, nil
}

func (r *SocialRepository) GetAllVloggers(ctx context.Context, FollowerID uint) ([]*account.Account, error) {
	var relations []Social
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("follower_id = ?", FollowerID).
		Find(&relations).Error; err != nil {
		return nil, err
	}

	vloggerIDs := make([]uint, 0, len(relations))
	for _, rel := range relations {
		vloggerIDs = append(vloggerIDs, rel.VloggerID)
	}
	if len(vloggerIDs) == 0 {
		return []*account.Account{}, nil
	}

	var vloggers []*account.Account
	if err := r.db.WithContext(ctx).
		Model(&account.Account{}).
		Where("id IN ?", vloggerIDs).
		Find(&vloggers).Error; err != nil {
		return nil, err
	}
	return vloggers, nil
}

func (r *SocialRepository) IsFollowed(ctx context.Context, social *Social) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).
		Model(&Social{}).
		Where("follower_id = ? AND vlogger_id = ?", social.FollowerID, social.VloggerID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
