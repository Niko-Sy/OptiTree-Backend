package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrProjectNotFound    = errors.New("项目不存在")
	ErrProjectNameEmpty   = errors.New("项目名称不能为空")
	ErrInvalidProjectType = errors.New("项目类型无效，应为 ft 或 kg")
)

type ProjectService struct {
	projectRepo *repository.ProjectRepository
	memberRepo  *repository.MemberRepository
	graphRepo   *repository.GraphRepository
	versionRepo *repository.VersionRepository
	docRepo     *repository.DocumentRepository
	rdb         *redis.Client
	cachePolicy CachePolicy
	db          *gorm.DB
}

func NewProjectService(
	db *gorm.DB,
	projectRepo *repository.ProjectRepository,
	memberRepo *repository.MemberRepository,
	graphRepo *repository.GraphRepository,
	versionRepo *repository.VersionRepository,
	docRepo *repository.DocumentRepository,
	rdb *redis.Client,
	cachePolicy CachePolicy,
) *ProjectService {
	cachePolicy = cachePolicy.normalize()
	return &ProjectService{
		db:          db,
		projectRepo: projectRepo,
		memberRepo:  memberRepo,
		graphRepo:   graphRepo,
		versionRepo: versionRepo,
		docRepo:     docRepo,
		rdb:         rdb,
		cachePolicy: cachePolicy,
	}
}

type CreateProjectInput struct {
	Name        string
	Type        string
	Description string
	Tags        []string
}

func (s *ProjectService) Create(ctx context.Context, userID string, input CreateProjectInput) (*model.Project, error) {
	if input.Type != constant.ProjectTypeFT && input.Type != constant.ProjectTypeKG {
		return nil, ErrInvalidProjectType
	}

	project := &model.Project{
		ID:          util.NewProjectID(),
		Name:        input.Name,
		Type:        input.Type,
		Description: &input.Description,
		Tags:        pq.StringArray(input.Tags),
		CreatedBy:   userID,
		MemberCount: 1,
	}
	if input.Description == "" {
		project.Description = nil
	}

	if err := s.projectRepo.Create(project); err != nil {
		return nil, err
	}

	// 创建者自动成为 admin
	member := &model.ProjectMember{
		ID:        util.NewMemberID(),
		ProjectID: project.ID,
		UserID:    userID,
		Role:      constant.RoleAdmin,
		Status:    constant.MemberStatusActive,
	}
	if err := s.memberRepo.Create(member); err != nil {
		return nil, err
	}

	s.invalidateProjectListCaches(ctx, []string{userID})

	return project, nil
}

func (s *ProjectService) GetByID(ctx context.Context, id string) (*model.Project, error) {
	project, err := s.projectRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrProjectNotFound
	}
	return project, nil
}

type ListProjectsInput struct {
	UserID    string
	Type      string
	Keyword   string
	SortBy    string
	SortOrder string
	Page      int
	PageSize  int
}

type projectListCachePayload struct {
	List  []model.Project `json:"list"`
	Total int64           `json:"total"`
}

func (s *ProjectService) List(ctx context.Context, input ListProjectsInput) ([]model.Project, int64, error) {
	params := repository.ProjectListParams{
		UserID:    input.UserID,
		Type:      input.Type,
		Keyword:   input.Keyword,
		SortBy:    input.SortBy,
		SortOrder: input.SortOrder,
		Page:      input.Page,
		PageSize:  input.PageSize,
	}

	if s.canUseProjectListCache() {
		cacheKey := buildProjectListCacheKey(params)
		if cached, err := s.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
			var payload projectListCachePayload
			if err := json.Unmarshal(cached, &payload); err == nil {
				return payload.List, payload.Total, nil
			}
		}

		projects, total, err := s.projectRepo.List(params)
		if err != nil {
			return nil, 0, err
		}

		payload := projectListCachePayload{List: projects, Total: total}
		if data, err := json.Marshal(payload); err == nil {
			_ = s.rdb.Set(ctx, cacheKey, data, s.cachePolicy.ProjectListTTL).Err()
			addProjectListCacheIndex(ctx, s.rdb, input.UserID, cacheKey, int64(s.cachePolicy.ProjectListIndexTTL/time.Second))
		}
		return projects, total, nil
	}

	return s.projectRepo.List(params)
}

type UpdateProjectInput struct {
	Name        string
	Description *string
	Tags        []string
}

type RenameProjectInput struct {
	Name string
}

func (s *ProjectService) Update(ctx context.Context, id string, input UpdateProjectInput) (*model.Project, error) {
	project, err := s.projectRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrProjectNotFound
	}

	project.Name = input.Name
	project.Description = input.Description
	project.Tags = pq.StringArray(input.Tags)

	if err := s.projectRepo.Update(project); err != nil {
		return nil, err
	}
	s.invalidateProjectListCachesByProject(ctx, id)
	return project, nil
}

func (s *ProjectService) Rename(ctx context.Context, id string, input RenameProjectInput) (*model.Project, error) {
	project, err := s.projectRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrProjectNotFound
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, ErrProjectNameEmpty
	}

	project.Name = name
	if err := s.projectRepo.Update(project); err != nil {
		return nil, err
	}
	s.invalidateProjectListCachesByProject(ctx, id)
	return project, nil
}

// Delete 事务删除项目及所有关联数据
func (s *ProjectService) Delete(ctx context.Context, id string) error {
	usersToInvalidate, _ := s.memberRepo.ListActiveUserIDsByProject(id)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.graphRepo.DeleteFaultTreeByProject(tx, id); err != nil {
			return err
		}
		if err := s.graphRepo.DeleteKnowledgeGraphByProject(tx, id); err != nil {
			return err
		}
		if err := s.versionRepo.DeleteByProject(tx, id); err != nil {
			return err
		}
		if err := s.docRepo.DeleteByProject(tx, id); err != nil {
			return err
		}
		if err := s.memberRepo.DeleteByProject(tx, id); err != nil {
			return err
		}
		return s.projectRepo.Delete(tx, id)
	})
	if err != nil {
		return err
	}

	s.invalidateProjectListCaches(ctx, usersToInvalidate)
	return nil
}

func (s *ProjectService) canUseProjectListCache() bool {
	return s.rdb != nil && s.cachePolicy.Enabled && s.cachePolicy.ProjectListEnabled
}

func (s *ProjectService) invalidateProjectListCaches(ctx context.Context, userIDs []string) {
	if !s.canUseProjectListCache() || len(userIDs) == 0 {
		return
	}
	invalidateProjectListCacheByUserIDs(ctx, s.rdb, userIDs)
}

func (s *ProjectService) invalidateProjectListCachesByProject(ctx context.Context, projectID string) {
	if !s.canUseProjectListCache() {
		return
	}
	userIDs, err := s.memberRepo.ListActiveUserIDsByProject(projectID)
	if err != nil {
		return
	}
	s.invalidateProjectListCaches(ctx, userIDs)
}

type DashboardSummary struct {
	FaultTreeProjectCount int64 `json:"faultTreeProjectCount"`
	KnowledgeProjectCount int64 `json:"knowledgeProjectCount"`
	FaultTreeNodeCount    int64 `json:"faultTreeNodeCount"`
	KnowledgeEntityCount  int64 `json:"knowledgeEntityCount"`
}

func (s *ProjectService) GetDashboardSummary(ctx context.Context, userID string) (*DashboardSummary, error) {
	ftCount, kgCount, err := s.projectRepo.CountByUser(userID)
	if err != nil {
		return nil, err
	}
	nodeSum, entitySum, err := s.projectRepo.SumNodeCounts(userID)
	if err != nil {
		return nil, err
	}
	return &DashboardSummary{
		FaultTreeProjectCount: ftCount,
		KnowledgeProjectCount: kgCount,
		FaultTreeNodeCount:    nodeSum,
		KnowledgeEntityCount:  entitySum,
	}, nil
}
