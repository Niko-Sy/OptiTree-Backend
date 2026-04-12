package graph_ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"
)

var ErrHybridPreviewInvalid = errors.New("hybrid preview invalid")

type HybridPreview struct {
	ToolName   string        `json:"toolName"`
	TotalItems int           `json:"totalItems"`
	Summary    string        `json:"summary"`
	Items      []PreviewItem `json:"items"`
}

type PreviewItem struct {
	ID         string          `json:"id"`
	NodeID     string          `json:"nodeId"`
	ChangeType string          `json:"changeType"`
	Before     interface{}     `json:"before"`
	After      interface{}     `json:"after"`
	Reason     string          `json:"reason"`
	Confidence float64         `json:"confidence"`
	ToolName   string          `json:"toolName,omitempty"`
	ToolArgs   json.RawMessage `json:"toolArgs,omitempty"`
}

func (p *HybridPreview) FilterByIDs(ids []string) *HybridPreview {
	if p == nil {
		return nil
	}
	if len(ids) == 0 {
		cpy := *p
		cpy.Items = append([]PreviewItem(nil), p.Items...)
		cpy.TotalItems = len(cpy.Items)
		return &cpy
	}
	allow := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			allow[id] = struct{}{}
		}
	}
	filtered := make([]PreviewItem, 0, len(p.Items))
	for _, item := range p.Items {
		if _, ok := allow[item.ID]; ok {
			filtered = append(filtered, item)
		}
	}
	return &HybridPreview{
		ToolName:   p.ToolName,
		TotalItems: len(filtered),
		Summary:    p.Summary,
		Items:      filtered,
	}
}

type HybridEngine struct {
	graphRepo   *repository.GraphRepository
	projectRepo *repository.ProjectRepository
	executor    *Executor
}

func NewHybridEngine(
	graphRepo *repository.GraphRepository,
	projectRepo *repository.ProjectRepository,
	executor *Executor,
) *HybridEngine {
	return &HybridEngine{
		graphRepo:   graphRepo,
		projectRepo: projectRepo,
		executor:    executor,
	}
}

func (e *HybridEngine) GeneratePreview(ctx context.Context, projectID, graphType, toolName string, args json.RawMessage) (*HybridPreview, error) {
	_ = ctx
	_ = args
	if strings.TrimSpace(graphType) != "faultTree" {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedGraph, graphType)
	}

	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "suggest_gate_corrections":
		return e.generateGateCorrectionPreview(projectID)
	case "suggest_batch_label_fix":
		return e.generateBatchLabelFixPreview(projectID)
	case "suggest_layout_optimization":
		return e.generateLayoutPreview(projectID)
	case "suggest_node_merge":
		return e.generateNodeMergePreview(projectID)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, toolName)
	}
}

func (e *HybridEngine) Commit(ctx context.Context, projectID, graphType string, preview *HybridPreview, approvedIDs []string) (*ExecuteResult, error) {
	if preview == nil {
		return nil, ErrHybridPreviewInvalid
	}
	if e.executor == nil {
		return nil, fmt.Errorf("%w: executor not configured", ErrHybridPreviewInvalid)
	}

	filtered := preview.FilterByIDs(approvedIDs)
	if filtered == nil || len(filtered.Items) == 0 {
		return &ExecuteResult{Summary: "未选择任何预览项", Patch: GraphPatch{}}, nil
	}

	ops := make([]map[string]interface{}, 0, len(filtered.Items))
	for _, item := range filtered.Items {
		if strings.TrimSpace(item.ToolName) == "" || len(item.ToolArgs) == 0 {
			continue
		}
		var argObj map[string]interface{}
		if err := json.Unmarshal(item.ToolArgs, &argObj); err != nil {
			continue
		}
		ops = append(ops, map[string]interface{}{
			"tool": strings.TrimSpace(item.ToolName),
			"args": argObj,
		})
	}

	if len(ops) == 0 {
		return &ExecuteResult{Summary: "预览项仅供参考，当前无可提交变更", Patch: GraphPatch{}}, nil
	}

	raw, err := json.Marshal(map[string]interface{}{"operations": ops})
	if err != nil {
		return nil, err
	}
	return e.executor.Execute(ctx, projectID, graphType, "batch_operations", raw)
}

func (e *HybridEngine) generateGateCorrectionPreview(projectID string) (*HybridPreview, error) {
	nodes, _, err := e.graphRepo.GetFaultTreeGraph(projectID)
	if err != nil {
		return nil, err
	}

	items := make([]PreviewItem, 0)
	for _, node := range nodes {
		if !strings.EqualFold(strings.TrimSpace(node.Type), "gate") {
			continue
		}
		if node.GateType == nil || !strings.EqualFold(strings.TrimSpace(*node.GateType), "AND") {
			continue
		}
		toolArgs, _ := json.Marshal(map[string]interface{}{
			"nodeId":   node.ID,
			"gateType": "OR",
		})
		items = append(items, PreviewItem{
			ID:         util.NewID("pi"),
			NodeID:     node.ID,
			ChangeType: "update_gate",
			Before:     map[string]interface{}{"gateType": "AND"},
			After:      map[string]interface{}{"gateType": "OR"},
			Reason:     "检测到 AND 门可能过于严格，建议转为 OR 门供人工确认",
			Confidence: 0.72,
			ToolName:   "update_gate",
			ToolArgs:   toolArgs,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].NodeID < items[j].NodeID
	})
	return &HybridPreview{
		ToolName:   "suggest_gate_corrections",
		TotalItems: len(items),
		Summary:    fmt.Sprintf("建议检查并修正 %d 个 AND 门", len(items)),
		Items:      items,
	}, nil
}

func (e *HybridEngine) generateBatchLabelFixPreview(projectID string) (*HybridPreview, error) {
	nodes, _, err := e.graphRepo.GetFaultTreeGraph(projectID)
	if err != nil {
		return nil, err
	}

	items := make([]PreviewItem, 0)
	for _, node := range nodes {
		normalized := normalizeLabel(node.Name)
		if normalized == strings.TrimSpace(node.Name) || normalized == "" {
			continue
		}
		toolArgs, _ := json.Marshal(map[string]interface{}{
			"nodeId": node.ID,
			"name":   normalized,
		})
		items = append(items, PreviewItem{
			ID:         util.NewID("pi"),
			NodeID:     node.ID,
			ChangeType: "update_node",
			Before:     map[string]interface{}{"name": node.Name},
			After:      map[string]interface{}{"name": normalized},
			Reason:     "统一节点标签格式（去首尾空白并压缩多空格）",
			Confidence: 0.95,
			ToolName:   "update_node",
			ToolArgs:   toolArgs,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].NodeID < items[j].NodeID
	})
	return &HybridPreview{
		ToolName:   "suggest_batch_label_fix",
		TotalItems: len(items),
		Summary:    fmt.Sprintf("建议标准化 %d 个节点标签", len(items)),
		Items:      items,
	}, nil
}

func (e *HybridEngine) generateLayoutPreview(projectID string) (*HybridPreview, error) {
	nodes, _, err := e.graphRepo.GetFaultTreeGraph(projectID)
	if err != nil {
		return nil, err
	}
	items := make([]PreviewItem, 0, len(nodes))
	for i, node := range nodes {
		newX := float64((i % 8) * 320)
		newY := float64((i / 8) * 140)
		items = append(items, PreviewItem{
			ID:         util.NewID("pi"),
			NodeID:     node.ID,
			ChangeType: "layout_preview",
			Before:     map[string]interface{}{"x": node.X, "y": node.Y},
			After:      map[string]interface{}{"x": newX, "y": newY},
			Reason:     "自动网格预览，仅供参考（当前不落库）",
			Confidence: 0.58,
		})
	}
	return &HybridPreview{
		ToolName:   "suggest_layout_optimization",
		TotalItems: len(items),
		Summary:    "已生成布局优化预览（当前版本仅预览，不直接提交）",
		Items:      items,
	}, nil
}

func (e *HybridEngine) generateNodeMergePreview(projectID string) (*HybridPreview, error) {
	nodes, _, err := e.graphRepo.GetFaultTreeGraph(projectID)
	if err != nil {
		return nil, err
	}

	byName := make(map[string][]string)
	for _, node := range nodes {
		key := strings.ToLower(strings.TrimSpace(node.Name))
		if key == "" {
			continue
		}
		byName[key] = append(byName[key], node.ID)
	}

	items := make([]PreviewItem, 0)
	for key, ids := range byName {
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		items = append(items, PreviewItem{
			ID:         util.NewID("pi"),
			NodeID:     ids[0],
			ChangeType: "merge_candidate",
			Before:     map[string]interface{}{"nodeIds": ids},
			After:      map[string]interface{}{"keepNodeId": ids[0]},
			Reason:     fmt.Sprintf("检测到重复标签候选: %s", key),
			Confidence: 0.66,
		})
	}

	return &HybridPreview{
		ToolName:   "suggest_node_merge",
		TotalItems: len(items),
		Summary:    fmt.Sprintf("检测到 %d 组可疑重复节点候选", len(items)),
		Items:      items,
	}, nil
}

func normalizeLabel(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	return strings.Join(parts, " ")
}
