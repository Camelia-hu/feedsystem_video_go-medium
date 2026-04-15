package rabbitmq

import (
	"context"
	"errors"
	"time"

	"feedsystem_video_go/internal/middleware/metrics"
)

type VideoMQ struct {
	*RabbitMQ
}

const (
	videoExchange   = "video.box.events"
	videoQueue      = "video.box.events"
	videoBindingKey = "video.box.*" // 重命名

	videoPublishInboxRK  = "video.box.publishInbox"
	videoPublishOutboxRK = "video.box.publishOutbox"
)

type VideoEvent struct {
	EventID    string    `json:"event_id"`
	Action     string    `json:"action"`
	VideoID    uint      `json:"video_id"`
	AuthorID   uint      `json:"author_id,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

func NewVideoMQ(base *RabbitMQ) (*VideoMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(videoExchange, videoQueue, videoBindingKey); err != nil {
		return nil, err
	}
	return &VideoMQ{RabbitMQ: base}, nil
}

func (v *VideoMQ) PublishInbox(ctx context.Context, videoID uint, authorID uint) error {
	return v.publish(ctx, "publish_inbox", videoPublishInboxRK, videoID, authorID)
}

func (v *VideoMQ) PublishOutbox(ctx context.Context, videoID uint, authorID uint) error {
	return v.publish(ctx, "publish_outbox", videoPublishOutboxRK, videoID, authorID)
}

func (v *VideoMQ) publish(ctx context.Context, action, routingKey string, videoID uint, authorID uint) error {
	if v == nil || v.RabbitMQ == nil {
		return errors.New("video mq is not initialized")
	}
	if videoID == 0 {
		return errors.New("videoID is required")
	}
	id, err := newEventID(16)
	if err != nil {
		return err
	}
	event := VideoEvent{
		EventID:    id,
		Action:     action,
		VideoID:    videoID,
		AuthorID:   authorID,
		OccurredAt: time.Now().UTC(),
	}
	err = v.PublishJSON(ctx, videoExchange, routingKey, event)
	// 记录 MQ 发布指标
	metrics.RecordMqPublish(videoQueue, err == nil)
	return err
}
