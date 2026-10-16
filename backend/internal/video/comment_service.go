package video

import (
	"context"
	"errors"
	"feedsystem_video_go/internal/middleware/metrics"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"strings"

	"gorm.io/gorm"
)

type CommentService struct {
	repo            *CommentRepository
	VideoRepository *VideoRepository
	cache           *rediscache.Client
	commentMQ       *rabbitmq.CommentMQ
	popularityMQ    *rabbitmq.PopularityMQ
}

func NewCommentService(repo *CommentRepository, videoRepo *VideoRepository, cache *rediscache.Client, commentMQ *rabbitmq.CommentMQ, popularityMQ *rabbitmq.PopularityMQ) *CommentService {
	return &CommentService{repo: repo, VideoRepository: videoRepo, cache: cache, commentMQ: commentMQ, popularityMQ: popularityMQ}
}

func (s *CommentService) Publish(ctx context.Context, comment *Comment) error {
	if comment == nil {
		return errors.New("comment is nil")
	}
	comment.Username = strings.TrimSpace(comment.Username)
	comment.Content = strings.TrimSpace(comment.Content)
	if comment.VideoID == 0 || comment.AuthorID == 0 {
		return errors.New("video_id and author_id are required")
	}
	if comment.Content == "" {
		return errors.New("content is required")
	}

	exists, err := s.VideoRepository.IsExist(ctx, comment.VideoID)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("video not found")
	}

	mysqlEnqueued := false
	redisEnqueued := false
	if s.commentMQ != nil {
		if err := s.commentMQ.Publish(ctx, comment.Username, comment.VideoID, comment.AuthorID, comment.Content); err == nil {
			mysqlEnqueued = true
		}
	}
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, comment.VideoID, 1); err == nil {
			redisEnqueued = true
		}
	}
	if mysqlEnqueued && redisEnqueued {
		// 记录评论发布指标
		metrics.CommentActionTotal.WithLabelValues("publish").Inc()
		return nil
	}

	// Fallback: direct MySQL write when comment MQ publish fails.
	if !mysqlEnqueued {
		if err := s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Select("id").First(&Video{}, comment.VideoID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errors.New("video not found")
				}
				return err
			}
			if err := tx.Create(comment).Error; err != nil {
				return err
			}
			return tx.Model(&Video{}).Where("id = ?", comment.VideoID).
				UpdateColumn("popularity", gorm.Expr("popularity + 1")).Error
		}); err != nil {
			return err
		}
	}

	// Fallback: direct Redis update when popularity MQ publish fails.
	if !redisEnqueued {
		UpdatePopularityCache(ctx, s.cache, comment.VideoID, 1)
	}
	return nil
}

func (s *CommentService) Delete(ctx context.Context, commentID uint, accountID uint) error {
	comment, err := s.repo.GetByID(ctx, commentID)
	if err != nil {
		return err
	}
	if comment == nil {
		return errors.New("comment not found")
	}
	if comment.AuthorID != accountID {
		return errors.New("permission denied")
	}

	mqEnqueued := false
	if s.commentMQ != nil {
		if err := s.commentMQ.Delete(ctx, commentID); err == nil {
			mqEnqueued = true
		}
	}
	if !mqEnqueued {
		if err := s.repo.DeleteComment(ctx, comment); err != nil {
			return err
		}
	}

	// 记录评论删除指标
	metrics.CommentActionTotal.WithLabelValues("delete").Inc()

	// 扣减视频热度（无论走 MQ 还是直写，popularity 都要 -1）
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, comment.VideoID, -1); err != nil {
			// MQ 失败时降级直写缓存
			UpdatePopularityCache(ctx, s.cache, comment.VideoID, -1)
		}
	} else {
		UpdatePopularityCache(ctx, s.cache, comment.VideoID, -1)
	}

	return nil
}

// AdminDelete 管理员删除评论（不检查作者权限）
func (s *CommentService) AdminDelete(ctx context.Context, commentID uint) error {
	comment, err := s.repo.GetByID(ctx, commentID)
	if err != nil {
		return err
	}
	if comment == nil {
		return errors.New("comment not found")
	}

	mqEnqueued := false
	if s.commentMQ != nil {
		if err := s.commentMQ.Delete(ctx, commentID); err == nil {
			mqEnqueued = true
		}
	}
	if !mqEnqueued {
		if err := s.repo.DeleteComment(ctx, comment); err != nil {
			return err
		}
	}

	// 记录评论删除指标
	metrics.CommentActionTotal.WithLabelValues("delete").Inc()

	// 扣减视频热度（无论走 MQ 还是直写，popularity 都要 -1）
	if s.popularityMQ != nil {
		if err := s.popularityMQ.Update(ctx, comment.VideoID, -1); err != nil {
			// MQ 失败时降级直写缓存
			UpdatePopularityCache(ctx, s.cache, comment.VideoID, -1)
		}
	} else {
		UpdatePopularityCache(ctx, s.cache, comment.VideoID, -1)
	}

	return nil
}

func (s *CommentService) GetAll(ctx context.Context, videoID uint) ([]Comment, error) {
	exists, err := s.VideoRepository.IsExist(ctx, videoID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("video not found")
	}
	return s.repo.GetAllComments(ctx, videoID)
}
