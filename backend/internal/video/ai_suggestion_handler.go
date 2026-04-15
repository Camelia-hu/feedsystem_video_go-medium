package video

import (
	"errors"
	"strconv"

	"feedsystem_video_go/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

// AISuggestionHandler 处理 Agentic 发布工作流相关的 HTTP 请求
type AISuggestionHandler struct {
	service *VideoService
}

func NewAISuggestionHandler(service *VideoService) *AISuggestionHandler {
	return &AISuggestionHandler{service: service}
}

// GetSuggestion 创作者查看 AI 为视频生成的内容建议
// GET /video/:id/ai-suggestion
func (h *AISuggestionHandler) GetSuggestion(c *gin.Context) {
	videoID, err := parseVideoID(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	suggestion, err := h.service.GetAISuggestion(c.Request.Context(), videoID)
	if err != nil {
		c.JSON(404, gin.H{"error": "AI suggestion not found"})
		return
	}

	c.JSON(200, suggestion)
}

// ConfirmSuggestion 创作者确认 AI 建议（Human-in-the-loop），将优化后的标题/标签写入视频
// POST /video/:id/ai-suggestion/confirm
func (h *AISuggestionHandler) ConfirmSuggestion(c *gin.Context) {
	videoID, err := parseVideoID(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// 校验当前用户是否为视频作者
	if err := h.checkOwnership(c, videoID); err != nil {
		c.JSON(403, gin.H{"error": err.Error()})
		return
	}

	suggestion, err := h.service.ConfirmAISuggestion(c.Request.Context(), videoID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{
		"message":    "AI suggestion confirmed and applied",
		"suggestion": suggestion,
	})
}

// RejectSuggestion 创作者拒绝 AI 建议，保留原始标题
// POST /video/:id/ai-suggestion/reject
func (h *AISuggestionHandler) RejectSuggestion(c *gin.Context) {
	videoID, err := parseVideoID(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := h.checkOwnership(c, videoID); err != nil {
		c.JSON(403, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.RejectAISuggestion(c.Request.Context(), videoID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "AI suggestion rejected"})
}

func parseVideoID(c *gin.Context) (uint, error) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}

func (h *AISuggestionHandler) checkOwnership(c *gin.Context, videoID uint) error {
	accountID, err := jwt.GetAccountID(c)
	if err != nil {
		return err
	}
	v, err := h.service.repo.GetByID(c.Request.Context(), videoID)
	if err != nil {
		return err
	}
	if v.AuthorID != accountID {
		return errors.New("forbidden: you are not the author of this video")
	}
	return nil
}
