package rabbitmq

import (
	"context"
	"errors"
	"time"

	"feedsystem_video_go/internal/middleware/metrics"
)

type OrchestrationMQ struct {
	*RabbitMQ
}

const (
	orchestrationExchange   = "content.orchestration.events"
	orchestrationQueue      = "content.orchestration.events"
	orchestrationBindingKey = "content.orchestration.*"
	orchestrationRoutingKey = "content.orchestration.analyze"
)

// OrchestrationEvent 视频内容编排事件，触发 AI Agent 分析
type OrchestrationEvent struct {
	EventID     string    `json:"event_id"`
	Action      string    `json:"action"`
	VideoID     uint      `json:"video_id"`
	AuthorID    uint      `json:"author_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func NewOrchestrationMQ(base *RabbitMQ) (*OrchestrationMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(orchestrationExchange, orchestrationQueue, orchestrationBindingKey); err != nil {
		return nil, err
	}
	return &OrchestrationMQ{RabbitMQ: base}, nil
}

// PublishAnalyze 发布内容分析事件，触发 Orchestrator Agent
func (o *OrchestrationMQ) PublishAnalyze(ctx context.Context, videoID, authorID uint, title, description string) error {
	if o == nil || o.RabbitMQ == nil {
		return errors.New("orchestration mq is not initialized")
	}
	if videoID == 0 {
		return errors.New("videoID is required")
	}
	id, err := newEventID(16)
	if err != nil {
		return err
	}
	event := OrchestrationEvent{
		EventID:     id,
		Action:      "analyze",
		VideoID:     videoID,
		AuthorID:    authorID,
		Title:       title,
		Description: description,
		OccurredAt:  time.Now().UTC(),
	}
	err = o.PublishJSON(ctx, orchestrationExchange, orchestrationRoutingKey, event)
	metrics.RecordMqPublish(orchestrationQueue, err == nil)
	return err
}
