package handler

import (
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type DashboardHandler struct {
	projectService *service.ProjectService
}

func NewDashboardHandler(projectService *service.ProjectService) *DashboardHandler {
	return &DashboardHandler{projectService: projectService}
}

func (h *DashboardHandler) GetSummary(c *gin.Context) {
	userID := middleware.GetUserID(c)
	summary, err := h.projectService.GetDashboardSummary(c.Request.Context(), userID)
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.Success(c, summary)
}
