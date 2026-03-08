package handler

import (
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

func (h *AITaskHandler) GetTask(c *gin.Context) {
	taskID := c.Param("taskId")
	task, err := h.aiTaskService.GetTask(c.Request.Context(), taskID)
	if err != nil || task == nil {
		util.FailNotFound(c)
		return
	}
	util.Success(c, gin.H{"task": task})
}
