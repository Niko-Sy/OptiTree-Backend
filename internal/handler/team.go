package handler

import (
	"strconv"

	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

// TeamHandler handles team overview and activity endpoints.
type TeamHandler struct {
	teamService *service.TeamService
}

func NewTeamHandler(teamService *service.TeamService) *TeamHandler {
	return &TeamHandler{teamService: teamService}
}

// GetOverview handles GET /team/overview
func (h *TeamHandler) GetOverview(c *gin.Context) {
	userID := middleware.GetUserID(c)
	overview, err := h.teamService.GetOverview(c.Request.Context(), userID)
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.Success(c, overview)
}

// ListProjects handles GET /team/projects
func (h *TeamHandler) ListProjects(c *gin.Context) {
	userID := middleware.GetUserID(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	items, total, err := h.teamService.ListProjects(c.Request.Context(), service.TeamProjectsParams{
		UserID:   userID,
		Keyword:  c.Query("keyword"),
		Type:     c.Query("type"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.PageSuccess(c, items, total, page, pageSize)
}

// ListActivities handles GET /team/activities
func (h *TeamHandler) ListActivities(c *gin.Context) {
	userID := middleware.GetUserID(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	activities, err := h.teamService.ListActivities(c.Request.Context(), userID, limit)
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.Success(c, gin.H{"list": activities})
}
