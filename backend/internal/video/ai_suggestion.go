package video

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// SuggestionStatus AI建议的状态
type SuggestionStatus string

const (
	SuggestionStatusPending   SuggestionStatus = "pending"
	SuggestionStatusConfirmed SuggestionStatus = "confirmed"
	SuggestionStatusRejected  SuggestionStatus = "rejected"
)

// VideoAISuggestion Agentic 发布工作流生成的内容建议
type VideoAISuggestion struct {
	ID               uint             `gorm:"primaryKey" json:"id"`
	VideoID          uint             `gorm:"index;not null" json:"video_id"`
	Status           SuggestionStatus `gorm:"type:varchar(20);not null;default:'pending'" json:"status"`
	OriginalTitle    string           `gorm:"type:varchar(255);not null" json:"original_title"`
	SuggestedTitle   string           `gorm:"type:varchar(255)" json:"suggested_title"`
	SuggestedTags    string           `gorm:"type:varchar(500)" json:"suggested_tags"`    // 逗号分隔
	EngagementScore  int              `gorm:"not null;default:0" json:"engagement_score"` // 0-100
	PublishTimeHint  string           `gorm:"type:varchar(255)" json:"publish_time_hint"`
	RawAgentResponse string           `gorm:"type:text" json:"raw_agent_response,omitempty"`
	CreatedAt        time.Time        `gorm:"autoCreateTime" json:"created_at"`
	ConfirmedAt      *time.Time       `json:"confirmed_at,omitempty"`
}

// AISuggestionRepository AI建议的数据访问层
type AISuggestionRepository struct {
	db *gorm.DB
}

func NewAISuggestionRepository(db *gorm.DB) *AISuggestionRepository {
	return &AISuggestionRepository{db: db}
}

func (r *AISuggestionRepository) Create(ctx context.Context, s *VideoAISuggestion) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *AISuggestionRepository) GetByVideoID(ctx context.Context, videoID uint) (*VideoAISuggestion, error) {
	if videoID == 0 {
		return nil, errors.New("videoID is required")
	}
	var s VideoAISuggestion
	if err := r.db.WithContext(ctx).
		Where("video_id = ?", videoID).
		Order("id DESC").
		First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *AISuggestionRepository) Confirm(ctx context.Context, videoID uint) (*VideoAISuggestion, error) {
	if videoID == 0 {
		return nil, errors.New("videoID is required")
	}
	now := time.Now()
	if err := r.db.WithContext(ctx).
		Model(&VideoAISuggestion{}).
		Where("video_id = ? AND status = ?", videoID, SuggestionStatusPending).
		Updates(map[string]any{
			"status":       SuggestionStatusConfirmed,
			"confirmed_at": now,
		}).Error; err != nil {
		return nil, err
	}
	return r.GetByVideoID(ctx, videoID)
}

func (r *AISuggestionRepository) Reject(ctx context.Context, videoID uint) error {
	if videoID == 0 {
		return errors.New("videoID is required")
	}
	return r.db.WithContext(ctx).
		Model(&VideoAISuggestion{}).
		Where("video_id = ? AND status = ?", videoID, SuggestionStatusPending).
		Update("status", SuggestionStatusRejected).Error
}
