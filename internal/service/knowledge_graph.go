package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type KnowledgeGraphData struct {
	Nodes []model.KnowledgeGraphNode `json:"rfNodes"`
	Edges []model.KnowledgeGraphEdge `json:"rfEdges"`
}

type KnowledgeGraphService struct {
	projectRepo *repository.ProjectRepository
	graphRepo   *repository.GraphRepository
	rdb         *redis.Client
	db          *gorm.DB
}

func NewKnowledgeGraphService(
	db *gorm.DB,
	projectRepo *repository.ProjectRepository,
	graphRepo *repository.GraphRepository,
	rdb *redis.Client,
) *KnowledgeGraphService {
	return &KnowledgeGraphService{db: db, projectRepo: projectRepo, graphRepo: graphRepo, rdb: rdb}
}

func kgCacheKey(projectID string, revision int) string {
	return fmt.Sprintf("%s%s:v%d", constant.RedisKeyGraphKG, projectID, revision)
}

func (s *KnowledgeGraphService) GetGraph(ctx context.Context, projectID string) (*KnowledgeGraphData, int, error) {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, 0, err
	}
	if project == nil {
		return nil, 0, ErrProjectNotFound
	}

	cacheKey := kgCacheKey(projectID, project.GraphRevision)
	if cached, err := s.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
		var graph KnowledgeGraphData
		if err := json.Unmarshal(cached, &graph); err == nil {
			return &graph, project.GraphRevision, nil
		}
	}

	nodes, edges, err := s.graphRepo.GetKnowledgeGraphGraph(projectID)
	if err != nil {
		return nil, 0, err
	}

	graph := &KnowledgeGraphData{Nodes: nodes, Edges: edges}
	if data, err := json.Marshal(graph); err == nil {
		_ = s.rdb.Set(ctx, cacheKey, data, 5*time.Minute)
	}

	return graph, project.GraphRevision, nil
}

type SaveKnowledgeGraphInput struct {
	Nodes    []model.KnowledgeGraphNode
	Edges    []model.KnowledgeGraphEdge
	Revision int
}

func (s *KnowledgeGraphService) SaveGraph(ctx context.Context, projectID string, input SaveKnowledgeGraphInput) (*SaveGraphResult, error) {
	var result SaveGraphResult

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := range input.Nodes {
			input.Nodes[i].ProjectID = projectID
		}
		for i := range input.Edges {
			input.Edges[i].ProjectID = projectID
		}

		if err := s.graphRepo.BatchReplaceKnowledgeGraph(tx, projectID, input.Nodes, input.Edges); err != nil {
			return err
		}

		newRevision := input.Revision + 1
		affected, err := s.projectRepo.UpdateRevision(projectID, input.Revision, newRevision)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrVersionConflict
		}

		if err := s.projectRepo.UpdateCounts(projectID, 0, 0, len(input.Nodes), len(input.Edges)); err != nil {
			return err
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

	pattern := fmt.Sprintf("%s%s:v*", constant.RedisKeyGraphKG, projectID)
	keys, _ := s.rdb.Keys(ctx, pattern).Result()
	if len(keys) > 0 {
		_ = s.rdb.Del(ctx, keys...).Err()
	}

	return &result, nil
}

func (s *KnowledgeGraphService) ValidateGraph(nodes []model.KnowledgeGraphNode, edges []model.KnowledgeGraphEdge) []map[string]interface{} {
	var issues []map[string]interface{}

	nodeMap := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		nodeMap[n.ID] = true
	}

	// 检查边引用的节点是否存在
	for _, e := range edges {
		if !nodeMap[e.SourceNodeID] {
			issues = append(issues, map[string]interface{}{
				"nodeId":  e.SourceNodeID,
				"level":   "error",
				"message": fmt.Sprintf("边 %s 的源节点不存在", e.ID),
				"code":    "MISSING_SOURCE_NODE",
			})
		}
		if !nodeMap[e.TargetNodeID] {
			issues = append(issues, map[string]interface{}{
				"nodeId":  e.TargetNodeID,
				"level":   "error",
				"message": fmt.Sprintf("边 %s 的目标节点不存在", e.ID),
				"code":    "MISSING_TARGET_NODE",
			})
		}
	}

	// 检查节点标签是否为空
	for _, n := range nodes {
		if n.Label == "" {
			issues = append(issues, map[string]interface{}{
				"nodeId":  n.ID,
				"level":   "warning",
				"message": "节点标签不能为空",
				"code":    "EMPTY_LABEL",
			})
		}
	}

	if issues == nil {
		issues = []map[string]interface{}{}
	}
	return issues
}

func (s *KnowledgeGraphService) ExportGraph(ctx context.Context, projectID string) (*KnowledgeGraphData, error) {
	nodes, edges, err := s.graphRepo.GetKnowledgeGraphGraph(projectID)
	if err != nil {
		return nil, err
	}
	return &KnowledgeGraphData{Nodes: nodes, Edges: edges}, nil
}
