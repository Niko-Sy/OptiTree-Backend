package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type AssistantHandler struct {
	assistantService *service.AssistantService
}

func NewAssistantHandler(assistantService *service.AssistantService) *AssistantHandler {
	return &AssistantHandler{assistantService: assistantService}
}

func (h *AssistantHandler) GetModels(c *gin.Context) {
	util.Success(c, gin.H{
		"list": []gin.H{
			{"value": "qwen3.5-flash", "label": "通义千问-3.5-Flash（推荐）", "recommended": true},
			{"value": "qwen-plus", "label": "通义千问-Plus"},
			{"value": "qwen-turbo", "label": "通义千问-Turbo（快速）"},
			{"value": "deepseek-v3", "label": "DeepSeek-V3"},
		},
	})
}

type createConversationRequest struct {
	ProjectID string `json:"projectId" binding:"required"`
	Type      string `json:"type" binding:"required,oneof=faultTree knowledgeGraph"`
}

func (h *AssistantHandler) CreateConversation(c *gin.Context) {
	var req createConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	conversation, err := h.assistantService.CreateConversation(c.Request.Context(), service.CreateConversationInput{
		ProjectID: req.ProjectID,
		UserID:    middleware.GetUserID(c),
		Type:      req.Type,
	})
	if err != nil {
		h.handleCommonError(c, err, false)
		return
	}
	util.Success(c, gin.H{"conversation": conversation})
}

func (h *AssistantHandler) ListConversations(c *gin.Context) {
	page, pageSize := util.GetPagination(c)
	list, total, err := h.assistantService.ListConversations(c.Request.Context(), service.ListConversationsInput{
		UserID:    middleware.GetUserID(c),
		ProjectID: c.Query("projectId"),
		Type:      c.Query("type"),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		h.handleCommonError(c, err, false)
		return
	}
	util.PageSuccess(c, list, total, page, pageSize)
}

func (h *AssistantHandler) GetMessageHistory(c *gin.Context) {
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			limit = v
		}
	}

	out, err := h.assistantService.GetMessageHistory(c.Request.Context(), service.MessageHistoryInput{
		ConversationID: c.Param("conversationId"),
		UserID:         middleware.GetUserID(c),
		Before:         c.Query("before"),
		Limit:          limit,
	})
	if err != nil {
		h.handleCommonError(c, err, false)
		return
	}

	util.Success(c, gin.H{
		"conversation": out.Conversation,
		"messages":     out.Messages,
		"nextCursor":   out.NextCursor,
		"hasMore":      out.HasMore,
	})
}

type sendMessageRequest struct {
	Message string `json:"message" binding:"required,max=2000"`
	Model   string `json:"model"`
}

func (h *AssistantHandler) SendMessage(c *gin.Context) {
	var req sendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	out, err := h.assistantService.SendMessage(c.Request.Context(), service.SendMessageInput{
		ConversationID: c.Param("conversationId"),
		UserID:         middleware.GetUserID(c),
		Message:        req.Message,
		Model:          req.Model,
	})
	if err != nil {
		h.handleCommonError(c, err, true)
		return
	}

	util.Success(c, gin.H{
		"conversationId":   out.ConversationID,
		"userMessage":      out.UserMessage,
		"assistantMessage": out.AssistantMessage,
		"reply":            out.AssistantMessage.Content,
		"suggestions":      out.Suggestions,
	})
}

func (h *AssistantHandler) SendMessageStream(c *gin.Context) {
	var req sendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		util.Fail(c, constant.CodeServerError, "streaming not supported by the server")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")

	writeEvent := func(payload map[string]interface{}) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(c.Writer, "data: %s\n\n", b)
		flusher.Flush()
	}

	out, err := h.assistantService.SendMessageStream(
		c.Request.Context(),
		service.SendMessageInput{
			ConversationID: c.Param("conversationId"),
			UserID:         middleware.GetUserID(c),
			Message:        req.Message,
			Model:          req.Model,
		},
		func(chunk string) {
			writeEvent(map[string]interface{}{"type": "content", "content": chunk})
		},
	)
	if err != nil {
		if out != nil && out.AssistantMessageID != "" {
			writeEvent(map[string]interface{}{
				"type":               "partial",
				"assistantMessageId": out.AssistantMessageID,
				"tokensUsed":         out.TokensUsed,
				"modelId":            out.ModelUsed,
				"isPartial":          out.IsPartial,
			})
		}
		writeEvent(map[string]interface{}{
			"type":    "error",
			"code":    mapErrorCode(err, true),
			"message": err.Error(),
		})
		return
	}

	writeEvent(map[string]interface{}{
		"type":               "done",
		"conversationId":     out.ConversationID,
		"userMessageId":      out.UserMessageID,
		"assistantMessageId": out.AssistantMessageID,
		"tokensUsed":         out.TokensUsed,
		"modelId":            out.ModelUsed,
		"isPartial":          out.IsPartial,
	})
}

func (h *AssistantHandler) DeleteConversation(c *gin.Context) {
	err := h.assistantService.DeleteConversation(c.Request.Context(), c.Param("conversationId"), middleware.GetUserID(c))
	if err != nil {
		h.handleCommonError(c, err, false)
		return
	}
	util.SuccessNoData(c)
}

func (h *AssistantHandler) handleCommonError(c *gin.Context, err error, aiFailure bool) {
	switch {
	case errors.Is(err, service.ErrProjectNotFound), errors.Is(err, service.ErrAssistantConversationNotFound):
		util.FailNotFound(c)
	case errors.Is(err, service.ErrAssistantPermissionDenied):
		util.FailForbidden(c)
	case errors.Is(err, service.ErrAssistantInvalidType),
		errors.Is(err, service.ErrAssistantProjectTypeMismatch),
		errors.Is(err, service.ErrAssistantInvalidCursor),
		errors.Is(err, service.ErrAssistantMessageEmpty):
		util.Fail(c, constant.CodeInvalidParam, err.Error())
	default:
		if aiFailure {
			util.Fail(c, constant.CodeAIFailed, "AI 请求失败: "+err.Error())
			return
		}
		util.FailServerError(c)
	}
}

func mapErrorCode(err error, aiFailure bool) int {
	switch {
	case errors.Is(err, service.ErrProjectNotFound), errors.Is(err, service.ErrAssistantConversationNotFound):
		return constant.CodeNotFound
	case errors.Is(err, service.ErrAssistantPermissionDenied):
		return constant.CodeForbidden
	case errors.Is(err, service.ErrAssistantInvalidType),
		errors.Is(err, service.ErrAssistantProjectTypeMismatch),
		errors.Is(err, service.ErrAssistantInvalidCursor),
		errors.Is(err, service.ErrAssistantMessageEmpty):
		return constant.CodeInvalidParam
	default:
		if aiFailure {
			return constant.CodeAIUnavailable
		}
		return constant.CodeServerError
	}
}
