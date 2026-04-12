package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"optitree-backend/internal/config"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type AssistantHandler struct {
	assistantService *service.AssistantService
	aiConfig         config.AIConfig
}

const assistantStreamHeartbeatInterval = 10 * time.Second

func NewAssistantHandler(assistantService *service.AssistantService, aiConfig config.AIConfig) *AssistantHandler {
	return &AssistantHandler{assistantService: assistantService, aiConfig: aiConfig}
}

func (h *AssistantHandler) GetModels(c *gin.Context) {
	items := buildAssistantModelItems(h.aiConfig)
	list := make([]gin.H, 0, len(items))
	for _, item := range items {
		entry := gin.H{
			"value": item.Value,
			"label": item.Label,
		}
		if item.Provider != "" {
			entry["provider"] = item.Provider
		}
		if item.Recommended {
			entry["recommended"] = true
		}
		list = append(list, entry)
	}

	util.Success(c, gin.H{
		"list": list,
	})
}

type assistantModelItem struct {
	Value       string
	Label       string
	Provider    string
	Recommended bool
}

func buildAssistantModelItems(aiCfg config.AIConfig) []assistantModelItem {
	fromConfig := buildAssistantModelItemsFromConfig(aiCfg)
	if len(fromConfig) > 0 {
		return fromConfig
	}
	return buildAssistantModelItemsFromProviders(aiCfg)
}

func buildAssistantModelItemsFromConfig(aiCfg config.AIConfig) []assistantModelItem {
	if len(aiCfg.Models) == 0 {
		return nil
	}

	out := make([]assistantModelItem, 0, len(aiCfg.Models))
	seen := make(map[string]struct{}, len(aiCfg.Models))
	for _, m := range aiCfg.Models {
		provider := strings.ToLower(strings.TrimSpace(m.Provider))
		value := strings.TrimSpace(m.Value)
		if value == "" {
			value = strings.TrimSpace(m.Model)
		}
		if value == "" {
			continue
		}

		if provider != "" {
			lower := strings.ToLower(value)
			if !strings.HasPrefix(lower, provider+":") && !strings.HasPrefix(lower, provider+"/") {
				value = provider + ":" + value
			}
		}

		key := strings.ToLower(strings.TrimSpace(value))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		label := strings.TrimSpace(m.Label)
		if label == "" {
			label = value
		}
		out = append(out, assistantModelItem{
			Value:       value,
			Label:       label,
			Provider:    provider,
			Recommended: m.Recommended,
		})
	}
	return out
}

func buildAssistantModelItemsFromProviders(aiCfg config.AIConfig) []assistantModelItem {
	if len(aiCfg.Providers) == 0 {
		fallbackModel := strings.TrimSpace(aiCfg.ChatModel)
		if fallbackModel == "" {
			fallbackModel = strings.TrimSpace(aiCfg.DefaultModel)
		}
		if fallbackModel == "" {
			return []assistantModelItem{}
		}
		return []assistantModelItem{{
			Value:       fallbackModel,
			Label:       fallbackModel,
			Recommended: true,
		}}
	}

	normalizedProviders := make(map[string]config.AIProviderConfig, len(aiCfg.Providers))
	for rawKey, providerCfg := range aiCfg.Providers {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if key == "" {
			continue
		}
		normalizedProviders[key] = providerCfg
	}
	if len(normalizedProviders) == 0 {
		return []assistantModelItem{}
	}

	defaultProvider := strings.ToLower(strings.TrimSpace(aiCfg.DefaultProvider))
	providerKeys := make([]string, 0, len(normalizedProviders))
	for provider := range normalizedProviders {
		providerKeys = append(providerKeys, strings.ToLower(strings.TrimSpace(provider)))
	}
	sort.Strings(providerKeys)
	if defaultProvider == "" {
		if _, ok := normalizedProviders["qwen"]; ok {
			defaultProvider = "qwen"
		} else {
			defaultProvider = providerKeys[0]
		}
	}

	out := make([]assistantModelItem, 0, len(providerKeys)*2)
	seen := make(map[string]struct{}, len(providerKeys)*2)
	for _, provider := range providerKeys {
		cfg := normalizedProviders[provider]
		preferredModel := strings.TrimSpace(cfg.ChatModel)
		if preferredModel == "" {
			preferredModel = strings.TrimSpace(cfg.DefaultModel)
		}
		candidates := make([]string, 0, 2+len(cfg.ModelMaxCompletion))
		if s := strings.TrimSpace(cfg.ChatModel); s != "" {
			candidates = append(candidates, s)
		}
		if s := strings.TrimSpace(cfg.DefaultModel); s != "" {
			candidates = append(candidates, s)
		}
		for model := range cfg.ModelMaxCompletion {
			model = strings.TrimSpace(model)
			if model != "" {
				candidates = append(candidates, model)
			}
		}

		for _, model := range candidates {
			value := model
			if provider != "" && provider != defaultProvider {
				value = provider + ":" + model
			}
			key := strings.ToLower(strings.TrimSpace(value))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, assistantModelItem{
				Value:       value,
				Label:       fmt.Sprintf("%s - %s", strings.ToUpper(provider), model),
				Provider:    provider,
				Recommended: provider == defaultProvider && model == preferredModel,
			})
		}
	}

	if len(out) == 0 {
		return []assistantModelItem{}
	}
	return out
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

	streamCtx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	var mu sync.Mutex
	closed := false

	writeEvent := func(payload map[string]interface{}) bool {
		b, _ := json.Marshal(payload)
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", b); err != nil {
			closed = true
			cancel()
			return false
		}
		flusher.Flush()
		return true
	}

	if !writeEvent(map[string]interface{}{
		"type":           "started",
		"conversationId": c.Param("conversationId"),
	}) {
		return
	}

	heartbeatStop := make(chan struct{})
	var heartbeatWG sync.WaitGroup
	heartbeatWG.Add(1)
	go func() {
		defer heartbeatWG.Done()
		ticker := time.NewTicker(assistantStreamHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !writeEvent(map[string]interface{}{"type": "heartbeat"}) {
					return
				}
			case <-streamCtx.Done():
				return
			case <-heartbeatStop:
				return
			}
		}
	}()
	defer func() {
		close(heartbeatStop)
		heartbeatWG.Wait()
	}()

	out, err := h.assistantService.SendMessageStream(
		streamCtx,
		service.SendMessageInput{
			ConversationID: c.Param("conversationId"),
			UserID:         middleware.GetUserID(c),
			Message:        req.Message,
			Model:          req.Model,
		},
		func(chunk string) {
			_ = writeEvent(map[string]interface{}{"type": "content", "content": chunk})
		},
	)
	if err != nil {
		if out != nil && out.AssistantMessageID != "" {
			_ = writeEvent(map[string]interface{}{
				"type":               "partial",
				"assistantMessageId": out.AssistantMessageID,
				"tokensUsed":         out.TokensUsed,
				"modelId":            out.ModelUsed,
				"isPartial":          out.IsPartial,
			})
			// Partial text has been persisted; finish stream with done to avoid client auto-retry loops on generic error events.
			_ = writeEvent(map[string]interface{}{
				"type":               "done",
				"conversationId":     out.ConversationID,
				"userMessageId":      out.UserMessageID,
				"assistantMessageId": out.AssistantMessageID,
				"tokensUsed":         out.TokensUsed,
				"modelId":            out.ModelUsed,
				"isPartial":          true,
				"errorCode":          mapErrorCode(err, true),
				"errorMessage":       err.Error(),
			})
			return
		}
		_ = writeEvent(map[string]interface{}{
			"type":    "error",
			"code":    mapErrorCode(err, true),
			"message": err.Error(),
		})
		return
	}

	_ = writeEvent(map[string]interface{}{
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
