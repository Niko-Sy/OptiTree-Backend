package handler

import (
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type ProjectHandler struct {
	projectService *service.ProjectService
}

func NewProjectHandler(projectService *service.ProjectService) *ProjectHandler {
	return &ProjectHandler{projectService: projectService}
}

func (h *ProjectHandler) List(c *gin.Context) {
	page, pageSize := util.GetPagination(c)
	userID := middleware.GetUserID(c)

	projects, total, err := h.projectService.List(c.Request.Context(), service.ListProjectsInput{
		UserID:    userID,
		Type:      c.Query("type"),
		Keyword:   c.Query("keyword"),
		SortBy:    util.SafeSortBy(c.Query("sortBy"), []string{"created_at", "updated_at", "name"}, "created_at"),
		SortOrder: util.SafeSortOrder(c.Query("sortOrder")),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.PageSuccess(c, projects, total, page, pageSize)
}

type createProjectRequest struct {
	Name        string   `json:"name" binding:"required,max=100"`
	Type        string   `json:"type" binding:"required,oneof=ft kg"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

func (h *ProjectHandler) Create(c *gin.Context) {
	var req createProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	userID := middleware.GetUserID(c)
	project, err := h.projectService.Create(c.Request.Context(), userID, service.CreateProjectInput{
		Name:        req.Name,
		Type:        req.Type,
		Description: req.Description,
		Tags:        req.Tags,
	})
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.Success(c, gin.H{"project": project})
}

func (h *ProjectHandler) GetByID(c *gin.Context) {
	projectID := c.Param("projectId")
	project, err := h.projectService.GetByID(c.Request.Context(), projectID)
	if err != nil {
		if err == service.ErrProjectNotFound {
			util.FailNotFound(c)
			return
		}
		util.FailServerError(c)
		return
	}
	util.Success(c, gin.H{"project": project})
}

type updateProjectRequest struct {
	Name        string   `json:"name" binding:"required,max=100"`
	Description *string  `json:"description"`
	Tags        []string `json:"tags"`
}

func (h *ProjectHandler) Update(c *gin.Context) {
	var req updateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	projectID := c.Param("projectId")
	project, err := h.projectService.Update(c.Request.Context(), projectID, service.UpdateProjectInput{
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
	})
	if err != nil {
		switch err {
		case service.ErrProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"project": project})
}

func (h *ProjectHandler) Delete(c *gin.Context) {
	projectID := c.Param("projectId")
	if err := h.projectService.Delete(c.Request.Context(), projectID); err != nil {
		switch err {
		case service.ErrProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.SuccessNoData(c)
}
