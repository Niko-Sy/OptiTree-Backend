package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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
	cachePolicy CachePolicy
	db          *gorm.DB
}

func NewKnowledgeGraphService(
	db *gorm.DB,
	projectRepo *repository.ProjectRepository,
	graphRepo *repository.GraphRepository,
	rdb *redis.Client,
	cachePolicy CachePolicy,
) *KnowledgeGraphService {
	cachePolicy = cachePolicy.normalize()
	return &KnowledgeGraphService{db: db, projectRepo: projectRepo, graphRepo: graphRepo, rdb: rdb, cachePolicy: cachePolicy}
}

func kgCacheKey(projectID string) string {
	return fmt.Sprintf("%s%s", constant.RedisKeyGraphKG, projectID)
}

type knowledgeGraphCachePayload struct {
	Revision int                        `json:"revision"`
	Nodes    []model.KnowledgeGraphNode `json:"rfNodes"`
	Edges    []model.KnowledgeGraphEdge `json:"rfEdges"`
}

func (s *KnowledgeGraphService) GetGraph(ctx context.Context, projectID string) (*KnowledgeGraphData, int, error) {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, 0, err
	}
	if project == nil {
		return nil, 0, ErrProjectNotFound
	}

	cacheKey := kgCacheKey(projectID)
	if s.rdb != nil && s.cachePolicy.Enabled && s.cachePolicy.KnowledgeGraphEnabled {
		if cached, err := s.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
			var payload knowledgeGraphCachePayload
			if err := json.Unmarshal(cached, &payload); err == nil && payload.Revision == project.GraphRevision {
				return &KnowledgeGraphData{Nodes: payload.Nodes, Edges: payload.Edges}, project.GraphRevision, nil
			}
		}
	}

	nodes, edges, err := s.graphRepo.GetKnowledgeGraphGraph(projectID)
	if err != nil {
		return nil, 0, err
	}

	graph := &KnowledgeGraphData{Nodes: nodes, Edges: edges}
	if s.rdb != nil && s.cachePolicy.Enabled && s.cachePolicy.KnowledgeGraphEnabled {
		payload := knowledgeGraphCachePayload{
			Revision: project.GraphRevision,
			Nodes:    graph.Nodes,
			Edges:    graph.Edges,
		}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.rdb.Set(ctx, cacheKey, data, s.cachePolicy.KnowledgeGraphTTL).Err()
		}
	}

	return graph, project.GraphRevision, nil
}

type SaveKnowledgeGraphInput struct {
	Nodes    []model.KnowledgeGraphNode
	Edges    []model.KnowledgeGraphEdge
	Revision int
}

const knowledgeGraphIDMaxLen = 32

func normalizeKGNodeType(raw string) string {
	v := strings.TrimSpace(raw)
	switch v {
	case "entityNode", "eventNode", "causeNode":
		return v
	case "componentNode":
		// Frontend compatibility: componentNode is persisted as entityNode.
		return "entityNode"
	default:
		return "entityNode"
	}
}

func normalizeKGEntityType(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "component", "event", "cause", "other":
		return v
	case "componentnode", "entitynode":
		return "component"
	case "eventnode":
		return "event"
	case "causenode":
		return "cause"
	case "effect", "system", "process":
		// Keep compatibility with AI-generated extended categories.
		return "other"
	default:
		return "other"
	}
}

func normalizeKnowledgeGraphNodes(nodes []model.KnowledgeGraphNode) {
	for i := range nodes {
		nodes[i].Type = normalizeKGNodeType(nodes[i].Type)
		nodes[i].EntityType = normalizeKGEntityType(nodes[i].EntityType)

		if nodes[i].EntityType == "other" {
			switch nodes[i].Type {
			case "eventNode":
				nodes[i].EntityType = "event"
			case "causeNode":
				nodes[i].EntityType = "cause"
			default:
				nodes[i].EntityType = "component"
			}
		}
	}
}

func normalizeKGBoundedID(raw, prefix string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		v = prefix
	}
	if len(v) <= knowledgeGraphIDMaxLen {
		return v
	}

	sum := sha1.Sum([]byte(v))
	hash := hex.EncodeToString(sum[:])
	maxHashLen := knowledgeGraphIDMaxLen - len(prefix) - 1
	if maxHashLen < 8 {
		maxHashLen = 8
	}
	if maxHashLen > len(hash) {
		maxHashLen = len(hash)
	}
	return prefix + "_" + hash[:maxHashLen]
}

func normalizeKnowledgeGraphIDs(nodes []model.KnowledgeGraphNode, edges []model.KnowledgeGraphEdge) {
	nodeIDMap := make(map[string]string, len(nodes))
	for i := range nodes {
		oldID := strings.TrimSpace(nodes[i].ID)
		if oldID == "" {
			oldID = fmt.Sprintf("node_%d", i+1)
		}
		newID := normalizeKGBoundedID(oldID, "n")
		nodes[i].ID = newID
		nodeIDMap[oldID] = newID
	}

	for i := range edges {
		edgeID := strings.TrimSpace(edges[i].ID)
		if edgeID == "" {
			edgeID = fmt.Sprintf("edge_%d_%s_%s", i+1, edges[i].SourceNodeID, edges[i].TargetNodeID)
		}
		edges[i].ID = normalizeKGBoundedID(edgeID, "e")

		sourceID := strings.TrimSpace(edges[i].SourceNodeID)
		if mapped, ok := nodeIDMap[sourceID]; ok {
			edges[i].SourceNodeID = mapped
		} else {
			edges[i].SourceNodeID = normalizeKGBoundedID(sourceID, "n")
		}

		targetID := strings.TrimSpace(edges[i].TargetNodeID)
		if mapped, ok := nodeIDMap[targetID]; ok {
			edges[i].TargetNodeID = mapped
		} else {
			edges[i].TargetNodeID = normalizeKGBoundedID(targetID, "n")
		}
	}
}

func (s *KnowledgeGraphService) SaveGraph(ctx context.Context, projectID string, input SaveKnowledgeGraphInput) (*SaveGraphResult, error) {
	var result SaveGraphResult

	normalizeKnowledgeGraphNodes(input.Nodes)
	normalizeKnowledgeGraphIDs(input.Nodes, input.Edges)

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
		affected, err := s.projectRepo.UpdateGraphMetaCAS(
			tx,
			projectID,
			input.Revision,
			newRevision,
			0,
			0,
			len(input.Nodes),
			len(input.Edges),
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

	if s.rdb != nil && s.cachePolicy.Enabled && s.cachePolicy.KnowledgeGraphEnabled {
		_ = s.rdb.Del(ctx, kgCacheKey(projectID)).Err()
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
