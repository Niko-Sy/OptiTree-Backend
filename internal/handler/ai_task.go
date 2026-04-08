package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type AITaskHandler struct {
	aiTaskService *service.AITaskService
}

func NewAITaskHandler(aiTaskService *service.AITaskService) *AITaskHandler {
	return &AITaskHandler{aiTaskService: aiTaskService}
}

// GetModels handles GET /ai/models.
// This provides frontend-selectable model options for document parsing/generation flows.
func (h *AITaskHandler) GetModels(c *gin.Context) {
	util.Success(c, gin.H{
		"list": []gin.H{
			{"value": "qwen3.5-flash", "label": "通义千问-3.5-Flash（推荐）", "recommended": true},
			{"value": "qwen-plus", "label": "通义千问-Plus"},
			{"value": "qwen-turbo", "label": "通义千问-Turbo（快速）"},
			{"value": "deepseek-v3", "label": "DeepSeek-V3"},
		},
	})
}

// GetTask handles GET /ai/tasks/:taskId
func (h *AITaskHandler) GetTask(c *gin.Context) {
	taskID := c.Param("taskId")
	task, err := h.aiTaskService.GetTask(c.Request.Context(), taskID)
	if err != nil || task == nil {
		util.FailNotFound(c)
		return
	}
	util.Success(c, gin.H{"task": task})
}

// GetLatestFaultTreeTaskByProject handles GET /ai/projects/:projectId/tasks/latest.
func (h *AITaskHandler) GetLatestFaultTreeTaskByProject(c *gin.Context) {
	projectID := strings.TrimSpace(c.Param("projectId"))
	if projectID == "" {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, "projectId 不能为空")
		return
	}

	userID := middleware.GetUserID(c)
	task, err := h.aiTaskService.GetLatestFaultTreeTaskByProject(c.Request.Context(), projectID, userID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrProjectNotFound):
			util.FailNotFound(c)
		case errors.Is(err, service.ErrProjectTypeMismatch):
			util.Fail(c, constant.CodeInvalidParam, "projectId 类型与接口不匹配")
		case errors.Is(err, service.ErrProjectPermissionDenied):
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}

	if task == nil {
		util.Success(c, gin.H{"task": nil})
		return
	}
	util.Success(c, gin.H{"task": task})
}

// generateFaultTreeRequest is the body for POST /ai/fault-trees/generate.
type generateFaultTreeRequest struct {
	DocIDs    []string `json:"docIds" binding:"required,min=1"`
	TopEvent  string   `json:"topEvent" binding:"required,max=60"`
	ProjectID *string  `json:"projectId"`
	Config    struct {
		Quality  string `json:"quality"`
		Model    string `json:"model"`
		Depth    int    `json:"depth"`
		MaxNodes int    `json:"maxNodes"`
	} `json:"config"`
}

// GenerateFaultTree handles POST /ai/fault-trees/generate.
// Creates an async task and returns {taskId, status} immediately.
// The client polls GET /ai/tasks/:taskId for progress and the final result.
func (h *AITaskHandler) GenerateFaultTree(c *gin.Context) {
	var req generateFaultTreeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	userID := middleware.GetUserID(c)
	out, err := h.aiTaskService.GenerateFaultTree(c.Request.Context(), service.GenerateFaultTreeInput{
		DocIDs:    req.DocIDs,
		TopEvent:  req.TopEvent,
		ProjectID: req.ProjectID,
		Config: ai.GenerateConfig{
			Quality:  req.Config.Quality,
			Model:    req.Config.Model,
			Depth:    req.Config.Depth,
			MaxNodes: req.Config.MaxNodes,
		},
		UserID: userID,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrProjectNotFound):
			util.FailNotFound(c)
		case errors.Is(err, service.ErrProjectTypeMismatch):
			util.Fail(c, constant.CodeInvalidParam, "projectId 类型与接口不匹配")
		case errors.Is(err, service.ErrProjectPermissionDenied):
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, out)
}

// generateKGRequest is the body for POST /ai/knowledge-graphs/generate.
type generateKGRequest struct {
	DocIDs    []string `json:"docIds" binding:"required,min=1"`
	ProjectID *string  `json:"projectId"`
	Config    struct {
		Quality     string   `json:"quality"`
		Model       string   `json:"model"`
		EntityTypes []string `json:"entityTypes"`
	} `json:"config"`
}

// GenerateKnowledgeGraph handles POST /ai/knowledge-graphs/generate.
// Creates an async task and returns {taskId, status} immediately.
func (h *AITaskHandler) GenerateKnowledgeGraph(c *gin.Context) {
	var req generateKGRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	userID := middleware.GetUserID(c)
	out, err := h.aiTaskService.GenerateKnowledgeGraph(c.Request.Context(), service.GenerateKnowledgeGraphInput{
		DocIDs:    req.DocIDs,
		ProjectID: req.ProjectID,
		Config: ai.GenerateConfig{
			Quality:     req.Config.Quality,
			Model:       req.Config.Model,
			EntityTypes: req.Config.EntityTypes,
		},
		UserID: userID,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrProjectNotFound):
			util.FailNotFound(c)
		case errors.Is(err, service.ErrProjectTypeMismatch):
			util.Fail(c, constant.CodeInvalidParam, "projectId 类型与接口不匹配")
		case errors.Is(err, service.ErrProjectPermissionDenied):
			util.FailForbidden(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, out)
}

// chatRequest is the body for POST /ai/chat.
type chatRequest struct {
	Context interface{} `json:"context"`
	Type    string      `json:"type" binding:"required"`
	Message string      `json:"message" binding:"required"`
	Model   string      `json:"model"`
}

// Chat handles POST /ai/chat (synchronous).
func (h *AITaskHandler) Chat(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	resp, err := h.aiTaskService.Chat(c.Request.Context(), service.ChatInput{
		ContextData: req.Context,
		GraphType:   req.Type,
		Message:     req.Message,
		Model:       req.Model,
	})
	if err != nil {
		util.Fail(c, constant.CodeAIFailed, "AI 请求失败: "+err.Error())
		return
	}
	util.Success(c, resp)
}

// chatStreamRequest is the body for POST /ai/chat/stream.
type chatStreamRequest struct {
	Context     interface{} `json:"context"`
	ContextType string      `json:"contextType" binding:"required,oneof=faultTree knowledgeGraph"`
	Message     string      `json:"message"     binding:"required,max=2000"`
}

// ChatStream handles POST /ai/chat/stream.
// The response is a Server-Sent Events (SSE) stream. Each event is a JSON object on a
// "data:" line followed by two newlines.
//
// Content events:  data: {"type":"content","content":"<token>"}
// Done event:      data: {"type":"done","tokensUsed":512,"modelId":"qwen3"}
// Error event:     data: {"type":"error","code":50300,"message":"..."}
func (h *AITaskHandler) ChatStream(c *gin.Context) {
	var req chatStreamRequest
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

	// writeEvent serialises payload as JSON and flushes a single SSE data frame.
	writeEvent := func(payload map[string]interface{}) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(c.Writer, "data: %s\n\n", b)
		flusher.Flush()
	}

	tokensUsed, modelUsed, err := h.aiTaskService.ChatStream(
		c.Request.Context(),
		service.ChatStreamInput{
			ContextData: req.Context,
			ContextType: req.ContextType,
			Message:     req.Message,
		},
		func(chunk string) {
			writeEvent(map[string]interface{}{"type": "content", "content": chunk})
		},
	)
	if err != nil {
		writeEvent(map[string]interface{}{
			"type":    "error",
			"code":    constant.CodeAIUnavailable,
			"message": err.Error(),
		})
		return
	}
	writeEvent(map[string]interface{}{
		"type":       "done",
		"tokensUsed": tokensUsed,
		"modelId":    modelUsed,
	})
}

type aiTaskCallbackRequest struct {
	TaskID       string                 `json:"taskId" binding:"required"`
	ProjectID    string                 `json:"projectId"`
	TraceID      string                 `json:"traceId"`
	EventKey     string                 `json:"eventKey"`
	EventVersion int                    `json:"eventVersion"`
	Status       string                 `json:"status" binding:"required"`
	Progress     int                    `json:"progress"`
	Stage        string                 `json:"stage"`
	StageLabel   string                 `json:"stageLabel"`
	ErrorMessage string                 `json:"errorMessage"`
	Result       map[string]interface{} `json:"result"`
	WorkerID     string                 `json:"workerId"`
	Attempt      int                    `json:"attempt"`
}

// TaskCallback handles POST /internal/ai/tasks/callback.
// It is called by the Python worker after dequeuing/executing tasks.
func (h *AITaskHandler) TaskCallback(c *gin.Context) {
	token := strings.TrimSpace(c.GetHeader(h.aiTaskService.CallbackHeader()))

	rawBody, readErr := io.ReadAll(c.Request.Body)
	if readErr != nil {
		log.Error().Err(readErr).Str("requestId", c.GetString("requestId")).Msg("读取 callback body 失败")
		rawBody = []byte{}
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))

	bodyLog := string(rawBody)
	const maxBodyLogLen = 4096
	if len(bodyLog) > maxBodyLogLen {
		bodyLog = bodyLog[:maxBodyLogLen] + "...(truncated)"
	}

	log.Info().
		Str("requestId", c.GetString("requestId")).
		Str("ip", c.ClientIP()).
		Bool("hasToken", token != "").
		Int("tokenLength", len(token)).
		Str("callbackBody", bodyLog).
		Msg("收到 AI callback 请求")

	if !h.aiTaskService.AuthorizeCallback(token) {
		log.Warn().
			Str("requestId", c.GetString("requestId")).
			Str("ip", c.ClientIP()).
			Bool("hasToken", token != "").
			Int("tokenLength", len(token)).
			Msg("AI callback 鉴权失败")
		util.FailUnauthorized(c)
		return
	}

	var req aiTaskCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}

	err := h.aiTaskService.HandleTaskCallback(c.Request.Context(), service.TaskCallbackInput{
		TaskID:       req.TaskID,
		ProjectID:    req.ProjectID,
		TraceID:      req.TraceID,
		EventKey:     req.EventKey,
		EventVersion: req.EventVersion,
		Status:       req.Status,
		Progress:     req.Progress,
		Stage:        req.Stage,
		StageLabel:   req.StageLabel,
		ErrorMessage: req.ErrorMessage,
		Result:       req.Result,
		WorkerID:     req.WorkerID,
		Attempt:      req.Attempt,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAITaskCallbackTaskNotFound):
			util.FailNotFound(c)
		case errors.Is(err, service.ErrAITaskCallbackProjectMismatch):
			util.Fail(c, constant.CodeInvalidParam, "projectId 与任务不一致")
		case errors.Is(err, service.ErrAITaskCallbackInvalidStatus):
			util.Fail(c, constant.CodeInvalidParam, "status 非法")
		default:
			util.FailServerError(c)
		}
		return
	}

	util.SuccessNoData(c)
}
