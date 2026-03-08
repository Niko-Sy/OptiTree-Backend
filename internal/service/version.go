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
	"optitree-backend/internal/util"
)

var ErrVersionNotFound = errors.New("版本不存在")

type VersionService struct {
	versionRepo *repository.VersionRepository
	projectRepo *repository.ProjectRepository
	graphRepo   *repository.GraphRepository
	ftService   *FaultTreeService
	kgService   *KnowledgeGraphService
}

func NewVersionService(
	versionRepo *repository.VersionRepository,
	projectRepo *repository.ProjectRepository,
	graphRepo *repository.GraphRepository,
	ftService *FaultTreeService,
	kgService *KnowledgeGraphService,
) *VersionService {
	return &VersionService{
		versionRepo: versionRepo,
		projectRepo: projectRepo,
		graphRepo:   graphRepo,
		ftService:   ftService,
		kgService:   kgService,
	}
}

type CreateVersionInput struct {
	ProjectID string
	Label     string
	UserID    string
	Snapshot  interface{}
}

func (s *VersionService) Create(ctx context.Context, input CreateVersionInput) (*model.VersionSnapshot, error) {
	project, err := s.projectRepo.FindByID(input.ProjectID)
	if err != nil || project == nil {
		return nil, ErrProjectNotFound
	}

	label := input.Label
	if label == "" {
		label = fmt.Sprintf("版本 %s", time.Now().Format("2006/1/2 15:04:05"))
	}

	snapshotBytes, err := json.Marshal(input.Snapshot)
	if err != nil {
		return nil, err
	}

	version := &model.VersionSnapshot{
		ID:           util.NewVersionID(),
		ProjectID:    input.ProjectID,
		ProjectType:  project.Type,
		Label:        label,
		CreatedBy:    input.UserID,
		SnapshotJson: snapshotBytes,
	}

	if err := s.versionRepo.Create(version); err != nil {
		return nil, err
	}

	// 超过 30 条则删最旧
	count, _ := s.versionRepo.CountByProject(input.ProjectID)
	if count > constant.MaxVersionCount {
		oldest, _ := s.versionRepo.GetOldestByProject(input.ProjectID)
		if oldest != nil {
			_ = s.versionRepo.Delete(oldest.ID)
		}
	}

	// 更新项目最新版本ID
	_ = s.projectRepo.UpdateLatestVersion(input.ProjectID, &version.ID)

	return version, nil
}

func (s *VersionService) List(ctx context.Context, projectID string, page, pageSize int) ([]model.VersionSnapshot, int64, error) {
	return s.versionRepo.ListByProject(projectID, page, pageSize)
}

func (s *VersionService) GetDetail(ctx context.Context, projectID, versionID string) (*model.VersionSnapshot, error) {
	v, err := s.versionRepo.FindByID(versionID)
	if err != nil {
		return nil, err
	}
	if v == nil || v.ProjectID != projectID {
		return nil, ErrVersionNotFound
	}
	return v, nil
}

func (s *VersionService) Delete(ctx context.Context, projectID, versionID string) error {
	v, err := s.versionRepo.FindByID(versionID)
	if err != nil {
		return err
	}
	if v == nil || v.ProjectID != projectID {
		return ErrVersionNotFound
	}
	return s.versionRepo.Delete(versionID)
}

type RollbackInput struct {
	ProjectID              string
	VersionID              string
	UserID                 string
	CreateNewVersionBefore bool
}

func (s *VersionService) Rollback(ctx context.Context, input RollbackInput) error {
	v, err := s.versionRepo.FindByID(input.VersionID)
	if err != nil {
		return err
	}
	if v == nil || v.ProjectID != input.ProjectID {
		return ErrVersionNotFound
	}

	project, err := s.projectRepo.FindByID(input.ProjectID)
	if err != nil || project == nil {
		return ErrProjectNotFound
	}

	// 回滚前先保存当前状态
	if input.CreateNewVersionBefore {
		var currentSnapshot interface{}
		if project.Type == constant.ProjectTypeFT {
			graph, _, _ := s.ftService.GetGraph(ctx, input.ProjectID)
			currentSnapshot = graph
		} else {
			graph, _, _ := s.kgService.GetGraph(ctx, input.ProjectID)
			currentSnapshot = graph
		}
		_, _ = s.Create(ctx, CreateVersionInput{
			ProjectID: input.ProjectID,
			Label:     fmt.Sprintf("回滚前快照 %s", time.Now().Format("2006/1/2 15:04:05")),
			UserID:    input.UserID,
			Snapshot:  currentSnapshot,
		})
	}

	// 根据项目类型回滚
	if project.Type == constant.ProjectTypeFT {
		var graph FaultTreeGraph
		if err := json.Unmarshal(v.SnapshotJson, &graph); err != nil {
			return fmt.Errorf("解析版本快照失败: %w", err)
		}
		_, err = s.ftService.SaveGraph(ctx, input.ProjectID, SaveFaultTreeInput{
			Nodes:    graph.Nodes,
			Edges:    graph.Edges,
			Revision: project.GraphRevision,
		})
	} else {
		var graph KnowledgeGraphData
		if err := json.Unmarshal(v.SnapshotJson, &graph); err != nil {
			return fmt.Errorf("解析版本快照失败: %w", err)
		}
		_, err = s.kgService.SaveGraph(ctx, input.ProjectID, SaveKnowledgeGraphInput{
			Nodes:    graph.Nodes,
			Edges:    graph.Edges,
			Revision: project.GraphRevision,
		})
	}

	return err
}
