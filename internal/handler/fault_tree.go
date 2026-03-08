package handler

import (
	"encoding/json"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type FaultTreeHandler struct {
	ftService *service.FaultTreeService
}

func NewFaultTreeHandler(ftService *service.FaultTreeService) *FaultTreeHandler {
	return &FaultTreeHandler{ftService: ftService}
}

func (h *FaultTreeHandler) GetGraph(c *gin.Context) {
	projectID := c.Param("projectId")
	graph, revision, err := h.ftService.GetGraph(c.Request.Context(), projectID)
	if err != nil {
		switch err {
		case service.ErrProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, gin.H{
		"nodes":    graph.Nodes,
		"edges":    graph.Edges,
		"revision": revision,
	})
}

type saveFaultTreeRequest struct {
	Nodes    []model.FaultTreeNode `json:"nodes"`
	Edges    []model.FaultTreeEdge `json:"edges"`
	Revision int                   `json:"revision"`
}

func (h *FaultTreeHandler) SaveGraph(c *gin.Context) {
	var req saveFaultTreeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	projectID := c.Param("projectId")
	result, err := h.ftService.SaveGraph(c.Request.Context(), projectID, service.SaveFaultTreeInput{
		Nodes:    req.Nodes,
		Edges:    req.Edges,
		Revision: req.Revision,
	})
	if err != nil {
		switch err {
		case service.ErrVersionConflict:
			util.Fail(c, constant.CodeVersionConflict, constant.MsgVersionConflict)
		case service.ErrProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, result)
}

type validateFaultTreeRequest struct {
	Nodes []model.FaultTreeNode `json:"nodes"`
	Edges []model.FaultTreeEdge `json:"edges"`
}

func (h *FaultTreeHandler) Validate(c *gin.Context) {
	var req validateFaultTreeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	issues := h.ftService.ValidateGraph(req.Nodes, req.Edges)
	util.Success(c, gin.H{
		"valid":  len(issues) == 0,
		"issues": issues,
	})
}

func (h *FaultTreeHandler) ExportGraph(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	if format == "png" || format == "svg" {
		c.JSON(501, gin.H{
			"code":    501,
			"message": format + " export is not yet supported server-side; use the frontend canvas export instead",
		})
		return
	}
	projectID := c.Param("projectId")
	data, err := h.ftService.ExportGraph(c.Request.Context(), projectID)
	if err != nil {
		switch err {
		case service.ErrProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=fault_tree_export.json")
	_ = json.NewEncoder(c.Writer).Encode(data)
}

type importFaultTreeRequest struct {
	ProjectID string                `json:"projectId" binding:"required"`
	Nodes     []model.FaultTreeNode `json:"nodes"`
	Edges     []model.FaultTreeEdge `json:"edges"`
	Revision  int                   `json:"revision"`
}

func (h *FaultTreeHandler) ImportGraph(c *gin.Context) {
	var req importFaultTreeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	result, err := h.ftService.SaveGraph(c.Request.Context(), req.ProjectID, service.SaveFaultTreeInput{
		Nodes:    req.Nodes,
		Edges:    req.Edges,
		Revision: req.Revision,
	})
	if err != nil {
		switch err {
		case service.ErrVersionConflict:
			util.Fail(c, constant.CodeVersionConflict, constant.MsgVersionConflict)
		case service.ErrProjectNotFound:
			util.FailNotFound(c)
		default:
			util.FailServerError(c)
		}
		return
	}
	util.Success(c, result)
}
