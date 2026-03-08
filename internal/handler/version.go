package handler

import (
	"optitree-backend/internal/constant"
	"optitree-backend/internal/middleware"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type VersionHandler struct {
	versionService *service.VersionService
}

func NewVersionHandler(versionService *service.VersionService) *VersionHandler {
	return &VersionHandler{versionService: versionService}
}

func (h *VersionHandler) List(c *gin.Context) {
	projectID := c.Param("projectId")
	page, pageSize := util.GetPagination(c)
	versions, total, err := h.versionService.List(c.Request.Context(), projectID, page, pageSize)
	if err != nil {
		util.FailServerError(c)
		return
	}
	util.PageSuccess(c, versions, total, page, pageSize)
}

type createVersionRequest struct {
	Label    string      `json:"label"`
	Snapshot interface{} `json:"snapshot"`
}

func (h *VersionHandler) Create(c *gin.Context) {
	var req createVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	projectID := c.Param("projectId")
	userID := middleware.GetUserID(c)

	version, err := h.versionService.Create(c.Request.Context(), service.CreateVersionInput{
		ProjectID: projectID,
		Label:     req.Label,
		UserID:    userID,
		Snapshot:  req.Snapshot,
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
	util.Success(c, gin.H{"version": version})
}

func (h *VersionHandler) GetDetail(c *gin.Context) {
	projectID := c.Param("projectId")
	versionID := c.Param("versionId")
	version, err := h.versionService.GetDetail(c.Request.Context(), projectID, versionID)
	if err != nil {
		switch err {
		case service.ErrVersionNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{"version": version})
}

type rollbackVersionRequest struct {
	SaveCurrent bool `json:"saveCurrent"`
}

func (h *VersionHandler) Rollback(c *gin.Context) {
	var req rollbackVersionRequest
	_ = c.ShouldBindJSON(&req)

	projectID := c.Param("projectId")
	versionID := c.Param("versionId")
	userID := middleware.GetUserID(c)

	if err := h.versionService.Rollback(c.Request.Context(), service.RollbackInput{
		ProjectID:              projectID,
		VersionID:              versionID,
		UserID:                 userID,
		CreateNewVersionBefore: req.SaveCurrent,
	}); err != nil {
		switch err {
		case service.ErrVersionNotFound:
			util.FailNotFound(c)
		case service.ErrVersionConflict:
			util.Fail(c, constant.CodeVersionConflict, constant.MsgVersionConflict)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.SuccessNoData(c)
}

func (h *VersionHandler) Delete(c *gin.Context) {
	projectID := c.Param("projectId")
	versionID := c.Param("versionId")
	if err := h.versionService.Delete(c.Request.Context(), projectID, versionID); err != nil {
		switch err {
		case service.ErrVersionNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.SuccessNoData(c)
}
