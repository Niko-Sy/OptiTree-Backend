package handler

import (
	"optitree-backend/internal/ai"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type AITaskHandler struct {
	aiTaskService *service.AITaskService
}

func NewAITaskHandler(aiTaskService *service.AITaskService) *AITaskHandler {
	return &AITaskHandler{aiTaskService: aiTaskService}
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

// generateFaultTreeRequest is the body for POST /ai/fault-trees/generate.
type generateFaultTreeRequest struct {
	DocIDs   []string `json:"docIds" binding:"required,min=1"`
	TopEvent string   `json:"topEvent" binding:"required,max=60"`
	Config   struct {
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
		DocIDs:   req.DocIDs,
		TopEvent: req.TopEvent,
		Config: ai.GenerateConfig{
			Quality:  req.Config.Quality,
			Model:    req.Config.Model,
			Depth:    req.Config.Depth,
			MaxNodes: req.Config.MaxNodes,
		},
		UserID: userID,
	})
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.Success(c, out)
}

// generateKGRequest is the body for POST /ai/knowledge-graphs/generate.
type generateKGRequest struct {
	DocIDs []string `json:"docIds" binding:"required,min=1"`
	Config struct {
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
		DocIDs: req.DocIDs,
		Config: ai.GenerateConfig{
			Quality: req.Config.Quality,
			Model:   req.Config.Model,
		},
		UserID: userID,
	})
	if err != nil {
		util.FailServerError(c)
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
