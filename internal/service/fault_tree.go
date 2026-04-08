package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrVersionConflict = errors.New("数据版本冲突")
	ErrGraphNotFound   = errors.New("图数据不存在")
)

type FaultTreeGraph struct {
	Nodes []model.FaultTreeNode `json:"nodes"`
	Edges []model.FaultTreeEdge `json:"edges"`
}

type FaultTreeService struct {
	projectRepo *repository.ProjectRepository
	graphRepo   *repository.GraphRepository
	rdb         *redis.Client
	cachePolicy CachePolicy
	db          *gorm.DB
}

func NewFaultTreeService(
	db *gorm.DB,
	projectRepo *repository.ProjectRepository,
	graphRepo *repository.GraphRepository,
	rdb *redis.Client,
	cachePolicy CachePolicy,
) *FaultTreeService {
	cachePolicy = cachePolicy.normalize()
	return &FaultTreeService{db: db, projectRepo: projectRepo, graphRepo: graphRepo, rdb: rdb, cachePolicy: cachePolicy}
}

func ftCacheKey(projectID string) string {
	return fmt.Sprintf("%s%s", constant.RedisKeyGraphFT, projectID)
}

type faultTreeCachePayload struct {
	Revision int                   `json:"revision"`
	Nodes    []model.FaultTreeNode `json:"nodes"`
	Edges    []model.FaultTreeEdge `json:"edges"`
}

// GetGraph 获取故障树，先查缓存，未命中则查 DB
func (s *FaultTreeService) GetGraph(ctx context.Context, projectID string) (*FaultTreeGraph, int, error) {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, 0, err
	}
	if project == nil {
		return nil, 0, ErrProjectNotFound
	}

	cacheKey := ftCacheKey(projectID)
	if s.rdb != nil && s.cachePolicy.Enabled && s.cachePolicy.FaultTreeGraphEnabled {
		if cached, err := s.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
			var payload faultTreeCachePayload
			if err := json.Unmarshal(cached, &payload); err == nil && payload.Revision == project.GraphRevision {
				return &FaultTreeGraph{Nodes: payload.Nodes, Edges: payload.Edges}, project.GraphRevision, nil
			}
		}
	}

	nodes, edges, err := s.graphRepo.GetFaultTreeGraph(projectID)
	if err != nil {
		return nil, 0, err
	}
	graph := &FaultTreeGraph{Nodes: nodes, Edges: edges}

	// 回填缓存
	if s.rdb != nil && s.cachePolicy.Enabled && s.cachePolicy.FaultTreeGraphEnabled {
		payload := faultTreeCachePayload{
			Revision: project.GraphRevision,
			Nodes:    graph.Nodes,
			Edges:    graph.Edges,
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.rdb.Set(ctx, cacheKey, data, s.cachePolicy.FaultTreeGraphTTL).Err()
		}
	}

	return graph, project.GraphRevision, nil
}

type SaveFaultTreeInput struct {
	Nodes    []model.FaultTreeNode
	Edges    []model.FaultTreeEdge
	Revision int
}

type SaveGraphResult struct {
	Revision  int       `json:"revision"`
	NodeCount int       `json:"nodeCount"`
	EdgeCount int       `json:"edgeCount"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SaveGraph 保存故障树（事务 + 乐观锁）
func (s *FaultTreeService) SaveGraph(ctx context.Context, projectID string, input SaveFaultTreeInput) (*SaveGraphResult, error) {
	var result SaveGraphResult

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 设置项目ID
		for i := range input.Nodes {
			input.Nodes[i].ProjectID = projectID
		}
		for i := range input.Edges {
			input.Edges[i].ProjectID = projectID
		}

		if err := s.graphRepo.BatchReplaceFaultTree(tx, projectID, input.Nodes, input.Edges); err != nil {
			return err
		}

		// 乐观锁 CAS
		newRevision := input.Revision + 1
		affected, err := s.projectRepo.UpdateGraphMetaCAS(
			tx,
			projectID,
			input.Revision,
			newRevision,
			len(input.Nodes),
			len(input.Edges),
			0,
			0,
		)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrVersionConflict
		}

		result.Revision = newRevision
		result.NodeCount = len(input.Nodes)
		result.EdgeCount = len(input.Edges)
		result.UpdatedAt = time.Now()
		return nil
	})

	if err != nil {
		return nil, err
	}

	if s.rdb != nil && s.cachePolicy.Enabled && s.cachePolicy.FaultTreeGraphEnabled {
		_ = s.rdb.Del(ctx, ftCacheKey(projectID)).Err()
	}

	return &result, nil
}

// ValidateGraph 校验故障树结构
func (s *FaultTreeService) ValidateGraph(nodes []model.FaultTreeNode, edges []model.FaultTreeEdge) []map[string]interface{} {
	var issues []map[string]interface{}

	nodeMap := make(map[string]*model.FaultTreeNode, len(nodes))
	for i := range nodes {
		nodeMap[nodes[i].ID] = &nodes[i]
	}

	// 1. 检查是否有且只有一个 topEvent
	topEventCount := 0
	for _, n := range nodes {
		if n.Type == "topEvent" {
			topEventCount++
		}
	}
	if topEventCount == 0 {
		issues = append(issues, map[string]interface{}{
			"nodeId":  "",
			"level":   "error",
			"message": "故障树缺少顶部事件",
			"code":    "MISSING_TOP_EVENT",
		})
	} else if topEventCount > 1 {
		issues = append(issues, map[string]interface{}{
			"nodeId":  "",
			"level":   "error",
			"message": "故障树只能有一个顶部事件",
			"code":    "MULTIPLE_TOP_EVENTS",
		})
	}

	// 2. 检查 basicEvent 的概率范围
	for _, n := range nodes {
		if n.Type == "basicEvent" && n.Probability != nil {
			if *n.Probability < 0 || *n.Probability > 1 {
				issues = append(issues, map[string]interface{}{
					"nodeId":  n.ID,
					"level":   "error",
					"message": fmt.Sprintf("节点 %s 的概率值超出范围 [0, 1]", n.Name),
					"code":    "INVALID_PROBABILITY",
				})
			}
		}
	}

	// 3. 检查孤立节点（无任何边连接的节点，topEvent 除外）
	connected := make(map[string]bool)
	for _, e := range edges {
		connected[e.FromNodeID] = true
		connected[e.ToNodeID] = true
	}
	for _, n := range nodes {
		if n.Type == "topEvent" {
			continue
		}
		if !connected[n.ID] && len(nodes) > 1 {
			issues = append(issues, map[string]interface{}{
				"nodeId":  n.ID,
				"level":   "warning",
				"message": fmt.Sprintf("节点 %s 未连接到任何边", n.Name),
				"code":    "ISOLATED_NODE",
			})
		}
	}

	// 4. 检查 gate 节点是否有 gateType
	for _, n := range nodes {
		if n.Type == "gate" && (n.GateType == nil || *n.GateType == "") {
			issues = append(issues, map[string]interface{}{
				"nodeId":  n.ID,
				"level":   "error",
				"message": fmt.Sprintf("逻辑门节点 %s 未设置门类型", n.Name),
				"code":    "UNCONNECTED_GATE",
			})
		}
	}

	if issues == nil {
		issues = []map[string]interface{}{}
	}
	return issues
}

// ExportGraph 导出故障树 JSON
func (s *FaultTreeService) ExportGraph(ctx context.Context, projectID string) (*FaultTreeGraph, error) {
	nodes, edges, err := s.graphRepo.GetFaultTreeGraph(projectID)
	if err != nil {
		return nil, err
	}
	return &FaultTreeGraph{Nodes: nodes, Edges: edges}, nil
}
