package handler

import (
	"encoding/json"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/service"
	"optitree-backend/internal/util"

	"github.com/gin-gonic/gin"
)

type KnowledgeGraphHandler struct {
	kgService *service.KnowledgeGraphService
}

func NewKnowledgeGraphHandler(kgService *service.KnowledgeGraphService) *KnowledgeGraphHandler {
	return &KnowledgeGraphHandler{kgService: kgService}
}

func (h *KnowledgeGraphHandler) GetGraph(c *gin.Context) {
	projectID := c.Param("projectId")
	graph, revision, err := h.kgService.GetGraph(c.Request.Context(), projectID)
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
		"rfNodes":  graph.Nodes,
		"rfEdges":  graph.Edges,
		"revision": revision,
	})
}

type saveKnowledgeGraphRequest struct {
	Nodes    []model.KnowledgeGraphNode `json:"rfNodes"`
	Edges    []model.KnowledgeGraphEdge `json:"rfEdges"`
	Revision int                        `json:"revision"`
}

func (h *KnowledgeGraphHandler) SaveGraph(c *gin.Context) {
	var req saveKnowledgeGraphRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	projectID := c.Param("projectId")
	result, err := h.kgService.SaveGraph(c.Request.Context(), projectID, service.SaveKnowledgeGraphInput{
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

type validateKnowledgeGraphRequest struct {
	Nodes []model.KnowledgeGraphNode `json:"rfNodes"`
	Edges []model.KnowledgeGraphEdge `json:"rfEdges"`
}

func (h *KnowledgeGraphHandler) Validate(c *gin.Context) {
	var req validateKnowledgeGraphRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	issues := h.kgService.ValidateGraph(req.Nodes, req.Edges)
	util.Success(c, gin.H{
		"valid":  len(issues) == 0,
		"issues": issues,
	})
}

func (h *KnowledgeGraphHandler) ExportGraph(c *gin.Context) {
	format := c.DefaultQuery("format", "json")
	if format == "png" || format == "svg" {
		c.JSON(501, gin.H{
			"code":    501,
			"message": format + " export is not yet supported server-side; use the frontend canvas export instead",
		})
		return
	}
	projectID := c.Param("projectId")
	data, err := h.kgService.ExportGraph(c.Request.Context(), projectID)
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
	c.Header("Content-Disposition", "attachment; filename=knowledge_graph_export.json")
	_ = json.NewEncoder(c.Writer).Encode(data)
}

type importKnowledgeGraphRequest struct {
	ProjectID string                     `json:"projectId" binding:"required"`
	Nodes     []model.KnowledgeGraphNode `json:"rfNodes"`
	Edges     []model.KnowledgeGraphEdge `json:"rfEdges"`
	Revision  int                        `json:"revision"`
}

func (h *KnowledgeGraphHandler) ImportGraph(c *gin.Context) {
	var req importKnowledgeGraphRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		util.FailWithErrors(c, constant.CodeInvalidParam, constant.MsgInvalidParam, err.Error())
		return
	}
	result, err := h.kgService.SaveGraph(c.Request.Context(), req.ProjectID, service.SaveKnowledgeGraphInput{
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
