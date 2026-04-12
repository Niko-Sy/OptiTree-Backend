package graph_ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrExecutorUnsupportedGraph = errors.New("executor unsupported graph type")
	ErrExecutorVersionConflict  = errors.New("executor graph version conflict")
	ErrExecutorProjectNotFound  = errors.New("executor project not found")
)

type GraphPatch struct {
	UpsertNodes []map[string]interface{} `json:"upsertNodes,omitempty"`
	DeleteNodes []string                 `json:"deleteNodes,omitempty"`
	UpsertEdges []map[string]interface{} `json:"upsertEdges,omitempty"`
	DeleteEdges []string                 `json:"deleteEdges,omitempty"`
}

type ExecuteResult struct {
	Summary string     `json:"summary"`
	Patch   GraphPatch `json:"patch"`
}

type Executor struct {
	graphRepo   *repository.GraphRepository
	projectRepo *repository.ProjectRepository
	auditRepo   *repository.AuditLogRepository
	db          *gorm.DB
	rdb         *redis.Client
}

func NewExecutor(
	graphRepo *repository.GraphRepository,
	projectRepo *repository.ProjectRepository,
	auditRepo *repository.AuditLogRepository,
	db *gorm.DB,
	rdb *redis.Client,
) *Executor {
	return &Executor{
		graphRepo:   graphRepo,
		projectRepo: projectRepo,
		auditRepo:   auditRepo,
		db:          db,
		rdb:         rdb,
	}
}

type operationResult struct {
	summary      string
	patch        GraphPatch
	changedNodes int
}

type ftaConstraintIssue struct {
	NodeID  string
	Level   string
	Code    string
	Message string
}

func (e *Executor) Execute(ctx context.Context, projectID, graphType, toolName string, args json.RawMessage) (*ExecuteResult, error) {
	projectID = strings.TrimSpace(projectID)
	graphType = strings.TrimSpace(graphType)
	toolName = strings.TrimSpace(toolName)
	if projectID == "" || graphType == "" || toolName == "" {
		return nil, fmt.Errorf("%w: missing projectID/graphType/toolName", ErrInvalidParameters)
	}
	if graphType != "faultTree" {
		return nil, fmt.Errorf("%w: %s", ErrExecutorUnsupportedGraph, graphType)
	}

	if err := ValidateParameters(toolName, args); err != nil {
		return nil, err
	}

	nodes, edges, err := e.graphRepo.GetFaultTreeGraph(projectID)
	if err != nil {
		return nil, err
	}
	state := newFaultTreeState(nodes, edges)

	opResult, err := e.applyOperation(state, projectID, toolName, args)
	if err != nil {
		return nil, err
	}

	if operationHasGraphChanges(opResult) {
		if err := enforceFaultTreeMutationSafety(state); err != nil {
			return nil, err
		}
		if err := e.persistFaultTree(ctx, projectID, toolName, opResult.summary, state.nodes, state.edges); err != nil {
			return nil, err
		}

		if e.rdb != nil {
			_ = e.rdb.Del(ctx, constant.RedisKeyGraphFT+projectID).Err()
		}
	}

	return &ExecuteResult{
		Summary: opResult.summary,
		Patch:   opResult.patch,
	}, nil
}

func (e *Executor) ExecuteFaultTreeSnapshot(
	ctx context.Context,
	projectID,
	toolName string,
	args json.RawMessage,
	baseNodes []model.FaultTreeNode,
	baseEdges []model.FaultTreeEdge,
	expectedRevision int,
) (*ExecuteResult, []model.FaultTreeNode, []model.FaultTreeEdge, int, error) {
	projectID = strings.TrimSpace(projectID)
	toolName = strings.TrimSpace(toolName)
	if projectID == "" || toolName == "" {
		return nil, nil, nil, expectedRevision, fmt.Errorf("%w: missing projectID/toolName", ErrInvalidParameters)
	}

	if err := ValidateParameters(toolName, args); err != nil {
		return nil, nil, nil, expectedRevision, err
	}

	state := newFaultTreeState(baseNodes, baseEdges)
	opResult, err := e.applyOperation(state, projectID, toolName, args)
	if err != nil {
		return nil, nil, nil, expectedRevision, err
	}

	nextRevision := expectedRevision
	if operationHasGraphChanges(opResult) {
		if err := enforceFaultTreeMutationSafety(state); err != nil {
			return nil, nil, nil, expectedRevision, err
		}
		nextRevision, err = e.persistFaultTreeWithExpectedRevision(
			ctx,
			projectID,
			toolName,
			opResult.summary,
			state.nodes,
			state.edges,
			expectedRevision,
		)
		if err != nil {
			return nil, nil, nil, expectedRevision, err
		}

		if e.rdb != nil {
			_ = e.rdb.Del(ctx, constant.RedisKeyGraphFT+projectID).Err()
		}
	}

	return &ExecuteResult{
		Summary: opResult.summary,
		Patch:   opResult.patch,
	}, append([]model.FaultTreeNode(nil), state.nodes...), append([]model.FaultTreeEdge(nil), state.edges...), nextRevision, nil
}

func (e *Executor) persistFaultTree(ctx context.Context, projectID, toolName, summary string, nodes []model.FaultTreeNode, edges []model.FaultTreeEdge) error {
	_, err := e.persistFaultTreeWithExpectedRevision(ctx, projectID, toolName, summary, nodes, edges, -1)
	return err
}

func (e *Executor) persistFaultTreeWithExpectedRevision(
	ctx context.Context,
	projectID,
	toolName,
	summary string,
	nodes []model.FaultTreeNode,
	edges []model.FaultTreeEdge,
	expectedRevision int,
) (int, error) {
	project, err := e.projectRepo.FindByID(projectID)
	if err != nil {
		return 0, err
	}
	if project == nil {
		return 0, ErrExecutorProjectNotFound
	}

	baseRevision := project.GraphRevision
	if expectedRevision >= 0 {
		if project.GraphRevision != expectedRevision {
			return 0, ErrExecutorVersionConflict
		}
		baseRevision = expectedRevision
	}

	for i := range nodes {
		nodes[i].ProjectID = projectID
	}
	for i := range edges {
		edges[i].ProjectID = projectID
	}

	err = e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := e.graphRepo.BatchReplaceFaultTree(tx, projectID, nodes, edges); err != nil {
			return err
		}

		affected, err := e.projectRepo.UpdateGraphMetaCAS(
			tx,
			projectID,
			baseRevision,
			baseRevision+1,
			len(nodes),
			len(edges),
			0,
			0,
		)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrExecutorVersionConflict
		}

		if e.auditRepo != nil {
			projectIDCopy := projectID
			audit := &model.AuditLog{
				ID:           util.NewAuditLogID(),
				OperatorName: "Agent",
				Action:       buildGraphAuditAction(toolName),
				ResourceType: "graph",
				ResourceID:   projectID,
				Summary:      summary,
				ProjectID:    &projectIDCopy,
			}
			if err := e.auditRepo.Create(tx, audit); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return baseRevision + 1, nil
}

func (e *Executor) applyOperation(state *faultTreeState, projectID, toolName string, args json.RawMessage) (*operationResult, error) {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "update_node":
		return e.applyUpdateNode(state, args)
	case "update_gate":
		return e.applyUpdateGate(state, args)
	case "add_node":
		return e.applyAddNode(state, args)
	case "add_gate":
		return e.applyAddGate(state, args)
	case "delete_node":
		return e.applyDeleteNode(state, args)
	case "move_node":
		return e.applyMoveNode(state, args)
	case "batch_operations":
		return e.applyBatchOperations(state, projectID, args)
	case "get_graph_snapshot":
		return e.applyGetGraphSnapshot(state, args)
	case "get_node_detail":
		return e.applyGetNodeDetail(state, args)
	case "get_subtree":
		return e.applyGetSubtree(state, args)
	case "check_gate_semantics":
		return e.applyCheckGateSemantics(state, args)
	case "validate_fta_constraints":
		return e.applyValidateFTAConstraints(state)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, toolName)
	}
}

func (e *Executor) applyUpdateNode(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		NodeID            string  `json:"nodeId"`
		Name              *string `json:"name"`
		Description       *string `json:"description"`
		InvestigateMethod *string `json:"investigateMethod"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}

	node, ok := state.getNode(req.NodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, req.NodeID)
	}

	changed := map[string]interface{}{"id": node.ID}
	changeCount := 0

	if req.Name != nil {
		value := SanitizeStringParam(*req.Name, 60)
		if value == "" {
			return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalidParameters)
		}
		node.Name = value
		changed["name"] = value
		changeCount++
	}
	if req.Description != nil {
		value := SanitizeStringParam(*req.Description, 2000)
		if value == "" {
			node.Description = nil
			changed["description"] = ""
		} else {
			node.Description = &value
			changed["description"] = value
		}
		changeCount++
	}
	if req.InvestigateMethod != nil {
		value := SanitizeStringParam(*req.InvestigateMethod, 2000)
		if value == "" {
			node.InvestigateMethod = nil
			changed["investigateMethod"] = ""
		} else {
			node.InvestigateMethod = &value
			changed["investigateMethod"] = value
		}
		changeCount++
	}

	if changeCount == 0 {
		return &operationResult{summary: "未检测到节点字段变更", patch: GraphPatch{}}, nil
	}

	return &operationResult{
		summary: fmt.Sprintf("已更新节点 %s", node.ID),
		patch: GraphPatch{
			UpsertNodes: []map[string]interface{}{changed},
		},
		changedNodes: 1,
	}, nil
}

func (e *Executor) applyUpdateGate(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		NodeID   string `json:"nodeId"`
		GateType string `json:"gateType"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}
	gateType := strings.ToUpper(strings.TrimSpace(req.GateType))
	if !isValidGateType(gateType) {
		return nil, fmt.Errorf("%w: gateType must be AND|OR|VOTE", ErrInvalidParameters)
	}

	node, ok := state.getNode(req.NodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, req.NodeID)
	}
	if !strings.EqualFold(strings.TrimSpace(node.Type), "gate") {
		return nil, fmt.Errorf("%w: update_gate only supports existing gate nodes", ErrOperationNotAllowed)
	}
	if node.GateType != nil && strings.EqualFold(strings.TrimSpace(*node.GateType), gateType) {
		return &operationResult{summary: fmt.Sprintf("逻辑门 %s 的 gateType 无变化", node.ID), patch: GraphPatch{}}, nil
	}
	node.GateType = &gateType

	return &operationResult{
		summary: fmt.Sprintf("已更新逻辑门 %s 为 %s", node.ID, gateType),
		patch: GraphPatch{UpsertNodes: []map[string]interface{}{{
			"id":       node.ID,
			"type":     node.Type,
			"gateType": gateType,
			"name":     node.Name,
		}}},
		changedNodes: 1,
	}, nil
}

func (e *Executor) applyAddNode(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		Name        string  `json:"name"`
		NodeType    string  `json:"nodeType"`
		Description *string `json:"description"`
		ParentID    *string `json:"parentId"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}

	nodeName := SanitizeStringParam(req.Name, 60)
	if nodeName == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidParameters)
	}
	nodeType := strings.TrimSpace(req.NodeType)
	if !isValidNodeType(nodeType) {
		return nil, fmt.Errorf("%w: nodeType invalid", ErrInvalidParameters)
	}
	if nodeType == "gate" {
		return nil, fmt.Errorf("%w: add_node does not allow gate, use add_gate instead", ErrOperationNotAllowed)
	}

	if nodeType == "topEvent" {
		if len(state.nodes) > 0 {
			return nil, fmt.Errorf("%w: topEvent can only be created on an empty tree", ErrOperationNotAllowed)
		}
		if req.ParentID != nil && strings.TrimSpace(*req.ParentID) != "" {
			return nil, fmt.Errorf("%w: topEvent cannot have a parent", ErrOperationNotAllowed)
		}
	} else {
		if req.ParentID == nil || strings.TrimSpace(*req.ParentID) == "" {
			return nil, fmt.Errorf("%w: parentId is required for non-topEvent nodes", ErrInvalidParameters)
		}
	}

	newNode := model.FaultTreeNode{
		ID:              util.NewID("ftn"),
		Type:            nodeType,
		Name:            nodeName,
		X:               0,
		Y:               0,
		Width:           220,
		Height:          80,
		Priority:        0,
		ShowProbability: false,
	}
	if req.Description != nil {
		value := SanitizeStringParam(*req.Description, 2000)
		if value != "" {
			newNode.Description = &value
		}
	}

	parentID := ""
	if req.ParentID != nil && strings.TrimSpace(*req.ParentID) != "" {
		parentID = strings.TrimSpace(*req.ParentID)
		parentNode, ok := state.getNode(parentID)
		if !ok {
			return nil, fmt.Errorf("%w: parent node %s", ErrNodeNotFound, parentID)
		}
		if strings.EqualFold(strings.TrimSpace(parentNode.Type), "basicEvent") {
			return nil, fmt.Errorf("%w: basicEvent cannot be a parent node", ErrOperationNotAllowed)
		}
		if !strings.EqualFold(strings.TrimSpace(parentNode.Type), "gate") {
			return nil, fmt.Errorf("%w: parent must be a gate for add_node, use add_gate first", ErrOperationNotAllowed)
		}
	}

	state.addNode(newNode)
	patch := GraphPatch{UpsertNodes: []map[string]interface{}{faultTreeNodePatch(newNode)}}
	summary := fmt.Sprintf("已新增节点 %s", newNode.ID)

	if parentID != "" {
		edge := model.FaultTreeEdge{
			ID:         util.NewID("fte"),
			FromNodeID: parentID,
			ToNodeID:   newNode.ID,
		}
		state.addEdge(edge)
		patch.UpsertEdges = append(patch.UpsertEdges, faultTreeEdgePatch(edge))
		summary = fmt.Sprintf("已新增节点 %s 并连接到父节点 %s", newNode.ID, parentID)
	}

	return &operationResult{summary: summary, patch: patch, changedNodes: 1}, nil
}

func (e *Executor) applyAddGate(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		GateType     string   `json:"gateType"`
		ParentID     string   `json:"parentId"`
		ParentNodeID string   `json:"parentNodeId"`
		ChildIDs     []string `json:"childIds"`
		ChildNodeIDs []string `json:"childNodeIds"`
		Children     []string `json:"children"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ParentID) == "" {
		req.ParentID = req.ParentNodeID
	}
	if len(req.ChildIDs) == 0 {
		req.ChildIDs = req.ChildNodeIDs
	}
	if len(req.ChildIDs) == 0 {
		req.ChildIDs = req.Children
	}

	gateType := strings.ToUpper(strings.TrimSpace(req.GateType))
	if !isValidGateType(gateType) {
		return nil, fmt.Errorf("%w: gateType invalid", ErrInvalidParameters)
	}
	parentID := strings.TrimSpace(req.ParentID)
	parentNode, ok := state.getNode(parentID)
	if !ok {
		return nil, fmt.Errorf("%w: parent node %s", ErrNodeNotFound, parentID)
	}
	if strings.EqualFold(strings.TrimSpace(parentNode.Type), "basicEvent") {
		return nil, fmt.Errorf("%w: basicEvent cannot be a parent node", ErrOperationNotAllowed)
	}
	if len(req.ChildIDs) == 0 {
		return nil, fmt.Errorf("%w: childIds is required", ErrInvalidParameters)
	}

	childSet := make(map[string]struct{}, len(req.ChildIDs))
	orderedChildren := make([]string, 0, len(req.ChildIDs))
	for _, childID := range req.ChildIDs {
		cid := strings.TrimSpace(childID)
		if cid == "" {
			continue
		}
		if _, dup := childSet[cid]; dup {
			continue
		}
		if cid == parentID {
			return nil, fmt.Errorf("%w: childIds cannot contain parentId", ErrInvalidParameters)
		}
		childNode, ok := state.getNode(cid)
		if !ok {
			return nil, fmt.Errorf("%w: child node %s", ErrNodeNotFound, cid)
		}
		if strings.EqualFold(strings.TrimSpace(childNode.Type), "topEvent") {
			return nil, fmt.Errorf("%w: topEvent cannot be rewired as a child", ErrOperationNotAllowed)
		}
		if _, hasEdge := state.findEdgeID(parentID, cid); !hasEdge {
			return nil, fmt.Errorf("%w: edge %s->%s does not exist, cannot rewire", ErrOperationNotAllowed, parentID, cid)
		}
		childSet[cid] = struct{}{}
		orderedChildren = append(orderedChildren, cid)
	}
	if len(orderedChildren) < 2 {
		return nil, fmt.Errorf("%w: add_gate requires at least 2 valid childIds", ErrInvalidParameters)
	}

	gateNode := model.FaultTreeNode{
		ID:              util.NewID("gate"),
		Type:            "gate",
		Name:            gateType,
		X:               0,
		Y:               0,
		Width:           220,
		Height:          80,
		Priority:        0,
		ShowProbability: false,
		GateType:        &gateType,
	}
	state.addNode(gateNode)

	patch := GraphPatch{UpsertNodes: []map[string]interface{}{faultTreeNodePatch(gateNode)}}
	deletedEdges := state.removeEdgesByFromTo(parentID, childSet)
	patch.DeleteEdges = append(patch.DeleteEdges, deletedEdges...)

	connects := []model.FaultTreeEdge{{
		ID:         util.NewID("fte"),
		FromNodeID: parentID,
		ToNodeID:   gateNode.ID,
	}}
	for _, cid := range orderedChildren {
		connects = append(connects, model.FaultTreeEdge{
			ID:         util.NewID("fte"),
			FromNodeID: gateNode.ID,
			ToNodeID:   cid,
		})
	}
	for _, edge := range connects {
		if state.addEdge(edge) {
			patch.UpsertEdges = append(patch.UpsertEdges, faultTreeEdgePatch(edge))
		}
	}

	return &operationResult{
		summary:      fmt.Sprintf("已新增逻辑门 %s，并将 %d 个子节点从 %s 重连到该逻辑门", gateNode.ID, len(orderedChildren), parentID),
		patch:        patch,
		changedNodes: 1,
	}, nil
}

func (e *Executor) applyDeleteNode(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		NodeID         string `json:"nodeId"`
		DeleteChildren bool   `json:"deleteChildren"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}

	nodeID := strings.TrimSpace(req.NodeID)
	node, ok := state.getNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}
	if strings.EqualFold(strings.TrimSpace(node.Type), "topEvent") {
		return nil, fmt.Errorf("%w: topEvent cannot be deleted", ErrOperationNotAllowed)
	}

	parentIDs := state.parentIDsOf(nodeID)
	childIDs := state.childIDsOf(nodeID)

	toDelete := map[string]struct{}{nodeID: {}}
	if req.DeleteChildren {
		for _, child := range state.collectDescendants(nodeID) {
			toDelete[child] = struct{}{}
		}
	}

	if !req.DeleteChildren && len(childIDs) > 0 && len(parentIDs) == 0 {
		return nil, fmt.Errorf("%w: cannot delete root-like node without deleting children", ErrOperationNotAllowed)
	}

	deletedNodes, deletedEdges := state.removeNodes(toDelete)
	if len(deletedNodes) == 0 {
		return nil, fmt.Errorf("%w: no node deleted", ErrOperationNotAllowed)
	}

	patch := GraphPatch{
		DeleteNodes: deletedNodes,
		DeleteEdges: deletedEdges,
	}
	if !req.DeleteChildren && len(parentIDs) > 0 && len(childIDs) > 0 {
		for _, pid := range parentIDs {
			for _, cid := range childIDs {
				if pid == cid {
					continue
				}
				edge := model.FaultTreeEdge{ID: util.NewID("fte"), FromNodeID: pid, ToNodeID: cid}
				if state.addEdge(edge) {
					patch.UpsertEdges = append(patch.UpsertEdges, faultTreeEdgePatch(edge))
				}
			}
		}
	}

	return &operationResult{
		summary:      fmt.Sprintf("已安全删除 %d 个节点", len(deletedNodes)),
		patch:        patch,
		changedNodes: len(deletedNodes),
	}, nil
}

func (e *Executor) applyMoveNode(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		NodeID      string `json:"nodeId"`
		NewParentID string `json:"newParentId"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}

	nodeID := strings.TrimSpace(req.NodeID)
	newParentID := strings.TrimSpace(req.NewParentID)
	if nodeID == "" || newParentID == "" {
		return nil, fmt.Errorf("%w: nodeId and newParentId are required", ErrInvalidParameters)
	}
	if nodeID == newParentID {
		return nil, fmt.Errorf("%w: node cannot be parent of itself", ErrInvalidParameters)
	}
	node, ok := state.getNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}
	newParent, ok := state.getNode(newParentID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, newParentID)
	}
	if strings.EqualFold(strings.TrimSpace(node.Type), "topEvent") {
		return nil, fmt.Errorf("%w: topEvent cannot be moved under another parent", ErrOperationNotAllowed)
	}
	if strings.EqualFold(strings.TrimSpace(newParent.Type), "basicEvent") {
		return nil, fmt.Errorf("%w: basicEvent cannot be a parent node", ErrOperationNotAllowed)
	}
	if !strings.EqualFold(strings.TrimSpace(newParent.Type), "gate") && !strings.EqualFold(strings.TrimSpace(node.Type), "gate") {
		return nil, fmt.Errorf("%w: non-gate parent can only attach gate nodes", ErrOperationNotAllowed)
	}
	descendantSet := make(map[string]struct{}, 8)
	for _, id := range state.collectDescendants(nodeID) {
		descendantSet[id] = struct{}{}
	}
	if _, bad := descendantSet[newParentID]; bad {
		return nil, fmt.Errorf("%w: moving node under its descendant would create a cycle", ErrOperationNotAllowed)
	}

	deletedEdgeIDs := state.removeIncomingEdges(nodeID)
	added := false
	newEdge := model.FaultTreeEdge{
		ID:         util.NewID("fte"),
		FromNodeID: newParentID,
		ToNodeID:   nodeID,
	}
	if state.addEdge(newEdge) {
		added = true
	}

	patch := GraphPatch{DeleteEdges: deletedEdgeIDs}
	if added {
		patch.UpsertEdges = append(patch.UpsertEdges, faultTreeEdgePatch(newEdge))
	}

	return &operationResult{
		summary:      fmt.Sprintf("已移动节点 %s 到父节点 %s", nodeID, newParentID),
		patch:        patch,
		changedNodes: 1,
	}, nil
}

func (e *Executor) applyBatchOperations(state *faultTreeState, projectID string, args json.RawMessage) (*operationResult, error) {
	var req struct {
		Operations []struct {
			Tool string          `json:"tool"`
			Args json.RawMessage `json:"args"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}
	if len(req.Operations) == 0 {
		return nil, fmt.Errorf("%w: operations empty", ErrInvalidParameters)
	}

	merged := GraphPatch{}
	summaries := make([]string, 0, len(req.Operations))
	changed := 0

	for _, op := range req.Operations {
		if strings.EqualFold(strings.TrimSpace(op.Tool), "batch_operations") {
			return nil, fmt.Errorf("%w: nested batch_operations is forbidden", ErrOperationNotAllowed)
		}
		if err := ValidateParameters(op.Tool, op.Args); err != nil {
			return nil, err
		}

		res, err := e.applyOperation(state, projectID, op.Tool, op.Args)
		if err != nil {
			return nil, err
		}
		changed += res.changedNodes
		summaries = append(summaries, res.summary)
		merged = mergePatch(merged, res.patch)
	}

	return &operationResult{
		summary:      fmt.Sprintf("已执行 %d 个批量操作", len(req.Operations)),
		patch:        merged,
		changedNodes: changed,
	}, nil
}

func (e *Executor) applyGetGraphSnapshot(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		MaxNodes int `json:"maxNodes"`
		MaxEdges int `json:"maxEdges"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, err
		}
	}

	if req.MaxNodes <= 0 {
		req.MaxNodes = len(state.nodes)
	}
	if req.MaxEdges <= 0 {
		req.MaxEdges = len(state.edges)
	}

	nodeLimit := minInt(req.MaxNodes, len(state.nodes))
	edgeLimit := minInt(req.MaxEdges, len(state.edges))

	nodes := make([]map[string]interface{}, 0, nodeLimit)
	for i := 0; i < nodeLimit; i++ {
		nodes = append(nodes, structToMap(state.nodes[i]))
	}
	edges := make([]map[string]interface{}, 0, edgeLimit)
	for i := 0; i < edgeLimit; i++ {
		edges = append(edges, structToMap(state.edges[i]))
	}

	payload := map[string]interface{}{
		"tool":              "get_graph_snapshot",
		"nodeCount":         len(state.nodes),
		"edgeCount":         len(state.edges),
		"returnedNodeCount": len(nodes),
		"returnedEdgeCount": len(edges),
		"truncated":         len(nodes) < len(state.nodes) || len(edges) < len(state.edges),
		"nodes":             nodes,
		"edges":             edges,
	}

	return &operationResult{summary: encodeReadToolPayload(payload), patch: GraphPatch{}, changedNodes: 0}, nil
}

func (e *Executor) applyGetNodeDetail(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		NodeID string `json:"nodeId"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}
	nodeID := strings.TrimSpace(req.NodeID)
	node, ok := state.getNode(nodeID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}

	incoming := make([]map[string]interface{}, 0)
	outgoing := make([]map[string]interface{}, 0)
	parents := make(map[string]struct{})
	children := make(map[string]struct{})
	for _, edge := range state.edges {
		if strings.TrimSpace(edge.ToNodeID) == nodeID {
			incoming = append(incoming, structToMap(edge))
			parents[strings.TrimSpace(edge.FromNodeID)] = struct{}{}
		}
		if strings.TrimSpace(edge.FromNodeID) == nodeID {
			outgoing = append(outgoing, structToMap(edge))
			children[strings.TrimSpace(edge.ToNodeID)] = struct{}{}
		}
	}

	payload := map[string]interface{}{
		"tool":          "get_node_detail",
		"node":          structToMap(*node),
		"incomingEdges": incoming,
		"outgoingEdges": outgoing,
		"parentNodeIds": sortedSetKeys(parents),
		"childNodeIds":  sortedSetKeys(children),
	}

	return &operationResult{summary: encodeReadToolPayload(payload), patch: GraphPatch{}, changedNodes: 0}, nil
}

func (e *Executor) applyGetSubtree(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		RootNodeID string `json:"rootNodeId"`
		MaxDepth   int    `json:"maxDepth"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, err
	}
	rootNodeID := strings.TrimSpace(req.RootNodeID)
	if _, ok := state.getNode(rootNodeID); !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, rootNodeID)
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = 3
	}

	childrenMap := make(map[string][]string)
	for _, edge := range state.edges {
		from := strings.TrimSpace(edge.FromNodeID)
		to := strings.TrimSpace(edge.ToNodeID)
		if from == "" || to == "" {
			continue
		}
		childrenMap[from] = append(childrenMap[from], to)
	}

	type nodeDepth struct {
		id    string
		depth int
	}
	queue := []nodeDepth{{id: rootNodeID, depth: 0}}
	visited := map[string]int{rootNodeID: 0}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= req.MaxDepth {
			continue
		}
		for _, child := range childrenMap[cur.id] {
			nextDepth := cur.depth + 1
			if d, ok := visited[child]; ok && d <= nextDepth {
				continue
			}
			visited[child] = nextDepth
			queue = append(queue, nodeDepth{id: child, depth: nextDepth})
		}
	}

	nodeIDs := make([]string, 0, len(visited))
	for id := range visited {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	nodes := make([]map[string]interface{}, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node, ok := state.getNode(id)
		if !ok {
			continue
		}
		item := structToMap(*node)
		item["depth"] = visited[id]
		nodes = append(nodes, item)
	}

	allowed := make(map[string]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		allowed[id] = struct{}{}
	}
	edges := make([]map[string]interface{}, 0)
	for _, edge := range state.edges {
		from := strings.TrimSpace(edge.FromNodeID)
		to := strings.TrimSpace(edge.ToNodeID)
		if _, ok := allowed[from]; !ok {
			continue
		}
		if _, ok := allowed[to]; !ok {
			continue
		}
		edges = append(edges, structToMap(edge))
	}

	payload := map[string]interface{}{
		"tool":       "get_subtree",
		"rootNodeId": rootNodeID,
		"maxDepth":   req.MaxDepth,
		"nodeCount":  len(nodes),
		"edgeCount":  len(edges),
		"nodes":      nodes,
		"edges":      edges,
	}

	return &operationResult{summary: encodeReadToolPayload(payload), patch: GraphPatch{}, changedNodes: 0}, nil
}

func (e *Executor) applyCheckGateSemantics(state *faultTreeState, args json.RawMessage) (*operationResult, error) {
	var req struct {
		NodeID string `json:"nodeId"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, err
		}
	}

	targetNodeID := strings.TrimSpace(req.NodeID)
	issues := make([]map[string]interface{}, 0)
	childCount := make(map[string]int)
	for _, edge := range state.edges {
		from := strings.TrimSpace(edge.FromNodeID)
		if from != "" {
			childCount[from]++
		}
	}

	checkNode := func(node model.FaultTreeNode) {
		if !strings.EqualFold(strings.TrimSpace(node.Type), "gate") {
			issues = append(issues, map[string]interface{}{
				"nodeId":  node.ID,
				"level":   "error",
				"code":    "NOT_GATE_NODE",
				"message": "目标节点不是 gate 类型",
			})
			return
		}

		if node.GateType == nil || !isValidGateType(strings.TrimSpace(*node.GateType)) {
			issues = append(issues, map[string]interface{}{
				"nodeId":  node.ID,
				"level":   "error",
				"code":    "INVALID_GATE_TYPE",
				"message": "gateType 为空或非法，必须为 AND/OR/VOTE",
			})
		}

		if childCount[node.ID] < 2 {
			issues = append(issues, map[string]interface{}{
				"nodeId":  node.ID,
				"level":   "warning",
				"code":    "GATE_CHILD_COUNT_TOO_LOW",
				"message": "gate 子节点数量建议不少于 2",
			})
		}
	}

	if targetNodeID != "" {
		node, ok := state.getNode(targetNodeID)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, targetNodeID)
		}
		checkNode(*node)
	} else {
		for _, node := range state.nodes {
			if strings.EqualFold(strings.TrimSpace(node.Type), "gate") {
				checkNode(node)
			}
		}
	}

	payload := map[string]interface{}{
		"tool":       "check_gate_semantics",
		"nodeId":     targetNodeID,
		"issueCount": len(issues),
		"issues":     issues,
	}
	return &operationResult{summary: encodeReadToolPayload(payload), patch: GraphPatch{}, changedNodes: 0}, nil
}

func (e *Executor) applyValidateFTAConstraints(state *faultTreeState) (*operationResult, error) {
	issues := evaluateFTAConstraintIssues(state)
	topEvents := collectTopEventIDs(state.nodes)

	payload := map[string]interface{}{
		"tool":        "validate_fta_constraints",
		"nodeCount":   len(state.nodes),
		"edgeCount":   len(state.edges),
		"topEventIds": topEvents,
		"issueCount":  len(issues),
		"issues":      toIssuePayload(issues),
	}

	return &operationResult{summary: encodeReadToolPayload(payload), patch: GraphPatch{}, changedNodes: 0}, nil
}

func enforceFaultTreeMutationSafety(state *faultTreeState) error {
	issues := evaluateFTAConstraintIssues(state)
	blocking := make([]ftaConstraintIssue, 0, len(issues))
	for _, issue := range issues {
		if isBlockingMutationIssue(issue) {
			blocking = append(blocking, issue)
		}
	}
	if len(blocking) == 0 {
		return nil
	}

	const maxIssueSummary = 5
	parts := make([]string, 0, minInt(maxIssueSummary, len(blocking)))
	for i := 0; i < len(blocking) && i < maxIssueSummary; i++ {
		item := blocking[i]
		if strings.TrimSpace(item.NodeID) != "" {
			parts = append(parts, fmt.Sprintf("%s(%s): %s", item.Code, item.NodeID, item.Message))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", item.Code, item.Message))
	}
	if len(blocking) > maxIssueSummary {
		parts = append(parts, fmt.Sprintf("... and %d more issues", len(blocking)-maxIssueSummary))
	}

	return fmt.Errorf("%w: %s", ErrOperationNotAllowed, strings.Join(parts, "; "))
}

func evaluateFTAConstraintIssues(state *faultTreeState) []ftaConstraintIssue {
	issues := make([]ftaConstraintIssue, 0)
	inDegree := make(map[string]int, len(state.nodes))
	outDegree := make(map[string]int, len(state.nodes))
	nodeTypeByID := make(map[string]string, len(state.nodes))
	for _, node := range state.nodes {
		nodeTypeByID[strings.TrimSpace(node.ID)] = strings.TrimSpace(node.Type)
	}

	for _, edge := range state.edges {
		from := strings.TrimSpace(edge.FromNodeID)
		to := strings.TrimSpace(edge.ToNodeID)
		if from == "" || to == "" {
			issues = append(issues, ftaConstraintIssue{Level: "error", Code: "EDGE_NODE_ID_EMPTY", Message: "边存在空的 from/to 节点 ID"})
			continue
		}
		fromType, fromOK := nodeTypeByID[from]
		toType, toOK := nodeTypeByID[to]
		if !fromOK {
			issues = append(issues, ftaConstraintIssue{NodeID: from, Level: "error", Code: "EDGE_SOURCE_NOT_FOUND", Message: "边的源节点不存在"})
			continue
		}
		if !toOK {
			issues = append(issues, ftaConstraintIssue{NodeID: to, Level: "error", Code: "EDGE_TARGET_NOT_FOUND", Message: "边的目标节点不存在"})
			continue
		}

		outDegree[from]++
		inDegree[to]++

		if strings.EqualFold(fromType, "basicEvent") {
			issues = append(issues, ftaConstraintIssue{NodeID: from, Level: "error", Code: "BASIC_EVENT_AS_PARENT", Message: "Basic Event 不能作为父节点继续分解"})
		}
		if strings.EqualFold(toType, "topEvent") {
			issues = append(issues, ftaConstraintIssue{NodeID: to, Level: "error", Code: "TOP_EVENT_AS_CHILD", Message: "Top Event 不能作为子节点"})
		}
		if strings.EqualFold(fromType, "gate") && strings.EqualFold(toType, "gate") {
			issues = append(issues, ftaConstraintIssue{NodeID: to, Level: "error", Code: "GATE_HAS_GATE_CHILD", Message: "Gate 节点不应直接连接 Gate 子节点"})
		}
	}

	topEvents := make([]string, 0)
	for _, node := range state.nodes {
		nodeType := strings.TrimSpace(node.Type)
		nodeID := strings.TrimSpace(node.ID)
		if nodeType == "topEvent" {
			topEvents = append(topEvents, nodeID)
			if inDegree[nodeID] > 0 {
				issues = append(issues, ftaConstraintIssue{NodeID: nodeID, Level: "error", Code: "TOP_EVENT_NOT_ROOT", Message: "Top Event 必须为根节点（入度应为 0）"})
			}
		}

		if nodeType == "basicEvent" && outDegree[nodeID] > 0 {
			issues = append(issues, ftaConstraintIssue{NodeID: nodeID, Level: "warning", Code: "BASIC_EVENT_HAS_CHILDREN", Message: "Basic Event 通常不应继续分解（出度应为 0）"})
		}

		if nodeType == "gate" {
			if node.GateType == nil || !isValidGateType(strings.TrimSpace(*node.GateType)) {
				issues = append(issues, ftaConstraintIssue{NodeID: nodeID, Level: "error", Code: "INVALID_GATE_TYPE", Message: "gateType 必须为 AND/OR/VOTE"})
			}
			if outDegree[nodeID] < 2 {
				issues = append(issues, ftaConstraintIssue{NodeID: nodeID, Level: "warning", Code: "GATE_CHILD_COUNT_TOO_LOW", Message: "gate 子节点数量建议不少于 2"})
			}
		}
	}

	if len(topEvents) == 0 {
		issues = append(issues, ftaConstraintIssue{Level: "error", Code: "MISSING_TOP_EVENT", Message: "故障树缺少 Top Event"})
	} else if len(topEvents) > 1 {
		issues = append(issues, ftaConstraintIssue{Level: "error", Code: "MULTIPLE_TOP_EVENTS", Message: "故障树应只有一个 Top Event"})
	}

	if hasDirectedCycle(state.edges) {
		issues = append(issues, ftaConstraintIssue{Level: "error", Code: "CYCLE_DETECTED", Message: "检测到有向环，FTA 结构应为无环图"})
	}

	return issues
}

func collectTopEventIDs(nodes []model.FaultTreeNode) []string {
	out := make([]string, 0)
	for _, node := range nodes {
		if strings.EqualFold(strings.TrimSpace(node.Type), "topEvent") {
			out = append(out, strings.TrimSpace(node.ID))
		}
	}
	sort.Strings(out)
	return out
}

func toIssuePayload(issues []ftaConstraintIssue) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(issues))
	for _, issue := range issues {
		out = append(out, map[string]interface{}{
			"nodeId":  issue.NodeID,
			"level":   issue.Level,
			"code":    issue.Code,
			"message": issue.Message,
		})
	}
	return out
}

func isBlockingMutationIssue(issue ftaConstraintIssue) bool {
	if strings.EqualFold(strings.TrimSpace(issue.Level), "error") {
		return true
	}
	switch strings.TrimSpace(issue.Code) {
	case "BASIC_EVENT_HAS_CHILDREN", "GATE_CHILD_COUNT_TOO_LOW":
		return true
	default:
		return false
	}
}

func operationHasGraphChanges(op *operationResult) bool {
	if op == nil {
		return false
	}
	if op.changedNodes > 0 {
		return true
	}
	return len(op.patch.UpsertNodes) > 0 || len(op.patch.DeleteNodes) > 0 || len(op.patch.UpsertEdges) > 0 || len(op.patch.DeleteEdges) > 0
}

func encodeReadToolPayload(payload map[string]interface{}) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func structToMap(v interface{}) map[string]interface{} {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func sortedSetKeys(input map[string]struct{}) []string {
	out := make([]string, 0, len(input))
	for k := range input {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(k))
	}
	sort.Strings(out)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func hasDirectedCycle(edges []model.FaultTreeEdge) bool {
	adj := make(map[string][]string)
	for _, edge := range edges {
		from := strings.TrimSpace(edge.FromNodeID)
		to := strings.TrimSpace(edge.ToNodeID)
		if from == "" || to == "" {
			continue
		}
		adj[from] = append(adj[from], to)
	}

	const (
		colorWhite = 0
		colorGray  = 1
		colorBlack = 2
	)
	color := make(map[string]int)

	var visit func(string) bool
	visit = func(node string) bool {
		color[node] = colorGray
		for _, next := range adj[node] {
			switch color[next] {
			case colorGray:
				return true
			case colorWhite:
				if visit(next) {
					return true
				}
			}
		}
		color[node] = colorBlack
		return false
	}

	for node := range adj {
		if color[node] != colorWhite {
			continue
		}
		if visit(node) {
			return true
		}
	}
	return false
}

func buildGraphAuditAction(toolName string) string {
	action := "graph.agent." + strings.TrimSpace(toolName)
	if len(action) > 60 {
		action = action[:60]
	}
	return action
}

func mergePatch(base GraphPatch, extra GraphPatch) GraphPatch {
	base.UpsertNodes = append(base.UpsertNodes, extra.UpsertNodes...)
	base.DeleteNodes = append(base.DeleteNodes, extra.DeleteNodes...)
	base.UpsertEdges = append(base.UpsertEdges, extra.UpsertEdges...)
	base.DeleteEdges = append(base.DeleteEdges, extra.DeleteEdges...)
	return base
}

func faultTreeNodePatch(n model.FaultTreeNode) map[string]interface{} {
	item := map[string]interface{}{
		"id":     n.ID,
		"type":   n.Type,
		"name":   n.Name,
		"x":      n.X,
		"y":      n.Y,
		"width":  n.Width,
		"height": n.Height,
	}
	if n.GateType != nil {
		item["gateType"] = *n.GateType
	}
	if n.Description != nil {
		item["description"] = *n.Description
	}
	if n.InvestigateMethod != nil {
		item["investigateMethod"] = *n.InvestigateMethod
	}
	return item
}

func faultTreeEdgePatch(e model.FaultTreeEdge) map[string]interface{} {
	return map[string]interface{}{
		"id":   e.ID,
		"from": e.FromNodeID,
		"to":   e.ToNodeID,
	}
}

type faultTreeState struct {
	nodes     []model.FaultTreeNode
	edges     []model.FaultTreeEdge
	nodeIndex map[string]int
}

func newFaultTreeState(nodes []model.FaultTreeNode, edges []model.FaultTreeEdge) *faultTreeState {
	state := &faultTreeState{
		nodes:     append([]model.FaultTreeNode(nil), nodes...),
		edges:     append([]model.FaultTreeEdge(nil), edges...),
		nodeIndex: make(map[string]int, len(nodes)),
	}
	state.rebuildIndex()
	return state
}

func (s *faultTreeState) rebuildIndex() {
	s.nodeIndex = make(map[string]int, len(s.nodes))
	for i := range s.nodes {
		s.nodeIndex[strings.TrimSpace(s.nodes[i].ID)] = i
	}
}

func (s *faultTreeState) getNode(id string) (*model.FaultTreeNode, bool) {
	idx, ok := s.nodeIndex[strings.TrimSpace(id)]
	if !ok {
		return nil, false
	}
	return &s.nodes[idx], true
}

func (s *faultTreeState) addNode(node model.FaultTreeNode) {
	s.nodes = append(s.nodes, node)
	s.nodeIndex[strings.TrimSpace(node.ID)] = len(s.nodes) - 1
}

func (s *faultTreeState) addEdge(edge model.FaultTreeEdge) bool {
	for _, e := range s.edges {
		if strings.EqualFold(e.FromNodeID, edge.FromNodeID) && strings.EqualFold(e.ToNodeID, edge.ToNodeID) {
			return false
		}
	}
	s.edges = append(s.edges, edge)
	return true
}

func (s *faultTreeState) findEdgeID(fromNodeID, toNodeID string) (string, bool) {
	fromNodeID = strings.TrimSpace(fromNodeID)
	toNodeID = strings.TrimSpace(toNodeID)
	for _, edge := range s.edges {
		if strings.EqualFold(strings.TrimSpace(edge.FromNodeID), fromNodeID) && strings.EqualFold(strings.TrimSpace(edge.ToNodeID), toNodeID) {
			return strings.TrimSpace(edge.ID), true
		}
	}
	return "", false
}

func (s *faultTreeState) removeEdgesByFromTo(fromNodeID string, toNodeIDs map[string]struct{}) []string {
	fromNodeID = strings.TrimSpace(fromNodeID)
	deleted := make([]string, 0)
	kept := make([]model.FaultTreeEdge, 0, len(s.edges))
	for _, edge := range s.edges {
		from := strings.TrimSpace(edge.FromNodeID)
		to := strings.TrimSpace(edge.ToNodeID)
		if strings.EqualFold(from, fromNodeID) {
			if _, ok := toNodeIDs[to]; ok {
				deleted = append(deleted, edge.ID)
				continue
			}
		}
		kept = append(kept, edge)
	}
	s.edges = kept
	return deleted
}

func (s *faultTreeState) removeNodes(ids map[string]struct{}) ([]string, []string) {
	deletedNodes := make([]string, 0, len(ids))
	deletedEdges := make([]string, 0)
	keptNodes := make([]model.FaultTreeNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		if _, ok := ids[strings.TrimSpace(node.ID)]; ok {
			deletedNodes = append(deletedNodes, node.ID)
			continue
		}
		keptNodes = append(keptNodes, node)
	}
	s.nodes = keptNodes
	s.rebuildIndex()

	keptEdges := make([]model.FaultTreeEdge, 0, len(s.edges))
	for _, edge := range s.edges {
		if _, ok := ids[strings.TrimSpace(edge.FromNodeID)]; ok {
			deletedEdges = append(deletedEdges, edge.ID)
			continue
		}
		if _, ok := ids[strings.TrimSpace(edge.ToNodeID)]; ok {
			deletedEdges = append(deletedEdges, edge.ID)
			continue
		}
		keptEdges = append(keptEdges, edge)
	}
	s.edges = keptEdges

	return deletedNodes, deletedEdges
}

func (s *faultTreeState) removeIncomingEdges(nodeID string) []string {
	nodeID = strings.TrimSpace(nodeID)
	deleted := make([]string, 0)
	kept := make([]model.FaultTreeEdge, 0, len(s.edges))
	for _, edge := range s.edges {
		if strings.TrimSpace(edge.ToNodeID) == nodeID {
			deleted = append(deleted, edge.ID)
			continue
		}
		kept = append(kept, edge)
	}
	s.edges = kept
	return deleted
}

func (s *faultTreeState) parentIDsOf(nodeID string) []string {
	nodeID = strings.TrimSpace(nodeID)
	set := make(map[string]struct{})
	for _, edge := range s.edges {
		if strings.TrimSpace(edge.ToNodeID) != nodeID {
			continue
		}
		pid := strings.TrimSpace(edge.FromNodeID)
		if pid == "" {
			continue
		}
		set[pid] = struct{}{}
	}
	return sortedSetKeys(set)
}

func (s *faultTreeState) childIDsOf(nodeID string) []string {
	nodeID = strings.TrimSpace(nodeID)
	set := make(map[string]struct{})
	for _, edge := range s.edges {
		if strings.TrimSpace(edge.FromNodeID) != nodeID {
			continue
		}
		cid := strings.TrimSpace(edge.ToNodeID)
		if cid == "" {
			continue
		}
		set[cid] = struct{}{}
	}
	return sortedSetKeys(set)
}

func (s *faultTreeState) collectDescendants(rootNodeID string) []string {
	rootNodeID = strings.TrimSpace(rootNodeID)
	if rootNodeID == "" {
		return nil
	}

	childrenMap := make(map[string][]string)
	for _, edge := range s.edges {
		from := strings.TrimSpace(edge.FromNodeID)
		to := strings.TrimSpace(edge.ToNodeID)
		if from == "" || to == "" {
			continue
		}
		childrenMap[from] = append(childrenMap[from], to)
	}

	visited := map[string]struct{}{rootNodeID: {}}
	queue := []string{rootNodeID}
	out := make([]string, 0)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range childrenMap[cur] {
			if _, ok := visited[child]; ok {
				continue
			}
			visited[child] = struct{}{}
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}
