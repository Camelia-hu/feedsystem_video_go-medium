package video

import "time"

type Video struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AuthorID    uint      `gorm:"index;not null" json:"author_id"`
	Username    string    `gorm:"type:varchar(255);not null" json:"username"`
	Title       string    `gorm:"type:varchar(255);not null" json:"title"`
	Tags        string    `gorm:"type:varchar(500);default:''" json:"tags,omitempty"` // 逗号分隔的标签，由 AI 建议确认后写入
	Description string    `gorm:"type:varchar(255);" json:"description,omitempty"`
	PlayURL     string    `gorm:"type:varchar(255);not null" json:"play_url"`
	CoverURL    string    `gorm:"type:varchar(255);not null" json:"cover_url"`
	CreateTime  time.Time `gorm:"autoCreateTime" json:"create_time"`
	LikesCount  int64     `gorm:"column:likes_count;not null;default:0" json:"likes_count"`
	Popularity  int64     `gorm:"column:popularity;not null;default:0" json:"popularity"`
}

// FollowFeedInbox 关注收件箱
type FollowFeedInbox struct {
	ID        int64     `gorm:"column:id;primaryKey"`
	UserID    int64     `gorm:"column:user_id;not null;index:idx_user_score;uniqueIndex:uk_user_post"` // 这条feed属于哪个用户
	PostID    int64     `gorm:"column:post_id;not null;uniqueIndex:uk_user_post"`
	AuthorID  int64     `gorm:"column:author_id;not null"`
	Score     int64     `gorm:"column:score;not null;index:idx_user_score,sort:desc"` // 排序值，可直接用时间戳或综合分
	IsRead    int8      `gorm:"column:is_read;default:0"`
	IsDeleted int8      `gorm:"column:is_deleted;default:0;index:idx_user_score"` // 用户主动隐藏
	CreatedAt time.Time `gorm:"column:created_at;not null;index:idx_user_created,sort:desc"`
}

// FeedOutbox 作者发出的Feed记录表
// 用于存储作者发布的内容列表，供粉丝fanout或拉取
type FeedOutbox struct {
	ID        int64     `gorm:"column:id;primaryKey"`                                                        // 主键ID
	AuthorID  int64     `gorm:"column:author_id;not null;uniqueIndex:uk_author_post;index:idx_author_score"` // 作者ID
	PostID    int64     `gorm:"column:post_id;not null;uniqueIndex:uk_author_post"`                          // 帖子ID
	Score     int64     `gorm:"column:score;not null;index:idx_author_score,sort:desc"`                      // 排序值，一般使用时间戳或推荐分
	CreatedAt time.Time `gorm:"column:created_at;not null"`                                                  // 创建时间
}

type PublishVideoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	PlayURL     string `json:"play_url"`
	CoverURL    string `json:"cover_url"`
}

type DeleteVideoRequest struct {
	ID uint `json:"id"`
}

type ListByAuthorIDRequest struct {
	AuthorID uint `json:"author_id"`
	Offset   int  `json:"offset,omitempty"`
}

type GetDetailRequest struct {
	ID uint `json:"id"`
}

type UpdateLikesCountRequest struct {
	ID         uint  `json:"id"`
	LikesCount int64 `json:"likes_count"`
}
