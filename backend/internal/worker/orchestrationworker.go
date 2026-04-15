package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	amqp "github.com/rabbitmq/amqp091-go"

	"feedsystem_video_go/internal/middleware/metrics"
	"feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/video"
)

const (
	// hotVideoDecayKey 与 popularity_cache.go 保持一致
	hotVideoDecayKey = "hot:video:decay"
	// agentMaxTokens Claude 工具调用循环最大 token 数
	agentMaxTokens = 1024
	// agentMaxRounds 防止无限循环
	agentMaxRounds = 5
)

// AgentSuggestion 是 Claude Agent 最终输出的结构化建议，用于 JSON 解析
type AgentSuggestion struct {
	SuggestedTitle  string `json:"suggested_title"`
	SuggestedTags   string `json:"suggested_tags"`
	EngagementScore int    `json:"engagement_score"`
	PublishTimeHint string `json:"publish_time_hint"`
}

// OrchestrationWorker 消费 content_orchestration 事件，驱动 Claude Agentic 工作流
type OrchestrationWorker struct {
	ch             *amqp.Channel
	videoRepo      *video.VideoRepository
	suggestionRepo *video.AISuggestionRepository
	cache          *rediscache.Client
	queue          string
	llmClient      anthropic.Client // 值类型，anthropic.NewClient() 返回值类型
}

func NewOrchestrationWorker(
	ch *amqp.Channel,
	videoRepo *video.VideoRepository,
	suggestionRepo *video.AISuggestionRepository,
	cache *rediscache.Client,
	queue string,
) *OrchestrationWorker {
	return &OrchestrationWorker{
		ch:             ch,
		videoRepo:      videoRepo,
		suggestionRepo: suggestionRepo,
		cache:          cache,
		queue:          queue,
		llmClient:      anthropic.NewClient(), // 自动读取 ANTHROPIC_API_KEY 环境变量
	}
}

func (w *OrchestrationWorker) Run(ctx context.Context) error {
	if w.ch == nil || w.videoRepo == nil {
		return errors.New("orchestration worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}

	deliveries, err := w.ch.Consume(w.queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return errors.New("deliveries channel closed")
			}
			w.handleDelivery(ctx, d)
		}
	}
}

func (w *OrchestrationWorker) handleDelivery(ctx context.Context, d amqp.Delivery) {
	start := time.Now()
	err := w.process(ctx, d.Body)
	success := err == nil

	metrics.RecordMqConsume(w.queue, "analyze", success, time.Since(start))

	if err != nil {
		log.Printf("orchestration worker: failed to process message: %v", err)
		_ = d.Nack(false, true)
		return
	}
	_ = d.Ack(false)
}

func (w *OrchestrationWorker) process(ctx context.Context, body []byte) error {
	var evt rabbitmq.OrchestrationEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil // 消息格式错误直接丢弃
	}
	if evt.VideoID == 0 {
		return nil
	}

	log.Printf("orchestration worker: running agent for video=%d title=%q", evt.VideoID, evt.Title)

	suggestion, err := w.runAgentLoop(ctx, evt)
	if err != nil {
		return fmt.Errorf("agent loop failed for video=%d: %w", evt.VideoID, err)
	}

	if err := w.suggestionRepo.Create(ctx, suggestion); err != nil {
		return fmt.Errorf("save suggestion failed for video=%d: %w", evt.VideoID, err)
	}

	log.Printf("orchestration worker: suggestion saved for video=%d (engagement=%d)", evt.VideoID, suggestion.EngagementScore)
	return nil
}

// runAgentLoop 驱动 Claude 工具调用循环，返回结构化建议
func (w *OrchestrationWorker) runAgentLoop(ctx context.Context, evt rabbitmq.OrchestrationEvent) (*video.VideoAISuggestion, error) {
	tools := w.buildTools()
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(w.buildPrompt(evt))),
	}

	var lastRespText string

	for round := 0; round < agentMaxRounds; round++ {
		resp, err := w.llmClient.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeHaiku4_5, // 轻量模型，降低成本
			MaxTokens: agentMaxTokens,
			Tools:     tools,
			Messages:  messages,
		})
		if err != nil {
			return nil, fmt.Errorf("llm call failed (round=%d): %w", round, err)
		}

		// 收集本轮文本输出
		for _, block := range resp.Content {
			if block.Type == "text" {
				lastRespText = block.AsText().Text
			}
		}

		if resp.StopReason == anthropic.StopReasonEndTurn {
			break
		}

		if resp.StopReason != anthropic.StopReasonToolUse {
			break
		}

		// 将 response 内容块转为 param 类型，用于构建 assistant 消息
		paramBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(resp.Content))
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				paramBlocks = append(paramBlocks, anthropic.NewTextBlock(block.AsText().Text))
			case "tool_use":
				tu := block.AsToolUse()
				paramBlocks = append(paramBlocks, anthropic.NewToolUseBlock(tu.ID, tu.Input, tu.Name))
			}
		}
		assistantMsg := anthropic.NewAssistantMessage(paramBlocks...)
		messages = append(messages, assistantMsg)

		toolResults := make([]anthropic.ContentBlockParamUnion, 0)
		for _, block := range resp.Content {
			if block.Type == "tool_use" {
				tu := block.AsToolUse()
				result := w.executeTool(ctx, tu.Name)
				toolResults = append(toolResults, anthropic.NewToolResultBlock(tu.ID, result, false))
			}
		}
		if len(toolResults) > 0 {
			messages = append(messages, anthropic.NewUserMessage(toolResults...))
		}
	}

	return w.parseSuggestion(evt, lastRespText), nil
}

// buildPrompt 构建初始 system prompt
func (w *OrchestrationWorker) buildPrompt(evt rabbitmq.OrchestrationEvent) string {
	return fmt.Sprintf(`你是一个短视频平台的内容优化 AI。

一位创作者刚刚上传了一个视频：
- 原始标题："%s"
- 视频描述："%s"

请按以下步骤完成分析：
1. 调用 get_trending_topics 工具，获取当前热榜视频信息
2. 结合热榜趋势和视频内容，输出一个 JSON（不要包含任何其他文字），格式如下：
{
  "suggested_title": "优化后的标题（中文，吸引人，≤30字）",
  "suggested_tags": "标签1,标签2,标签3",
  "engagement_score": 75,
  "publish_time_hint": "建议在工作日晚上8-10点发布，此时用户活跃度最高"
}`, evt.Title, evt.Description)
}

// buildTools 定义 Agent 可调用的工具列表
func (w *OrchestrationWorker) buildTools() []anthropic.ToolUnionParam {
	return []anthropic.ToolUnionParam{
		{
			OfTool: &anthropic.ToolParam{
				Name:        "get_trending_topics",
				Description: anthropic.String("获取当前平台热榜视频信息，包含视频ID、标题和热度分数。用于了解当前流行趋势。"),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: map[string]any{},
				},
			},
		},
	}
}

// executeTool 执行工具调用，返回结果字符串
func (w *OrchestrationWorker) executeTool(ctx context.Context, name string) string {
	switch name {
	case "get_trending_topics":
		return w.getTrendingTopics(ctx)
	default:
		return fmt.Sprintf(`{"error": "unknown tool: %s"}`, name)
	}
}

// getTrendingTopics 从 Redis hot:video:decay ZSET 读取热榜，回退到空列表
func (w *OrchestrationWorker) getTrendingTopics(ctx context.Context) string {
	if w.cache == nil {
		return `{"topics": [], "note": "热榜暂不可用"}`
	}

	// 取 top-10 热门视频 ID
	items, err := w.cache.ZRevRangeByScoreWithScores(ctx, hotVideoDecayKey, "+inf", "0", 10)
	if err != nil || len(items) == 0 {
		return `{"topics": [], "note": "暂无热榜数据"}`
	}

	type topicItem struct {
		VideoID uint    `json:"video_id"`
		Score   float64 `json:"score"`
		Title   string  `json:"title,omitempty"`
	}

	topics := make([]topicItem, 0, len(items))
	for _, item := range items {
		id, err := strconv.ParseUint(item.Member, 10, 64)
		if err != nil {
			continue
		}
		t := topicItem{VideoID: uint(id), Score: float64(item.Score)}

		// 尝试从 DB 获取标题，失败则留空
		if v, err := w.videoRepo.GetByID(ctx, uint(id)); err == nil {
			t.Title = v.Title
		}
		topics = append(topics, t)
	}

	b, _ := json.Marshal(map[string]any{"topics": topics})
	return string(b)
}

// parseSuggestion 解析 Claude 最终输出的 JSON 文本，容错处理
func (w *OrchestrationWorker) parseSuggestion(evt rabbitmq.OrchestrationEvent, rawText string) *video.VideoAISuggestion {
	s := &video.VideoAISuggestion{
		VideoID:          evt.VideoID,
		Status:           video.SuggestionStatusPending,
		OriginalTitle:    evt.Title,
		RawAgentResponse: rawText,
	}

	var ag AgentSuggestion
	if err := json.Unmarshal([]byte(rawText), &ag); err == nil {
		s.SuggestedTitle = ag.SuggestedTitle
		s.SuggestedTags = ag.SuggestedTags
		s.EngagementScore = ag.EngagementScore
		s.PublishTimeHint = ag.PublishTimeHint
	} else {
		// 解析失败时设置默认值，保证记录仍然入库
		log.Printf("orchestration worker: failed to parse agent suggestion for video=%d: %v", evt.VideoID, err)
		s.SuggestedTitle = evt.Title
		s.PublishTimeHint = "建议在晚高峰（20:00-22:00）发布"
	}

	return s
}
