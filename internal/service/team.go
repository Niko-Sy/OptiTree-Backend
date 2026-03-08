package service

import (
	"context"
	"time"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

// TeamService provides aggregated team-level queries for the Team page.
type TeamService struct {
	db *gorm.DB
}

func NewTeamService(db *gorm.DB) *TeamService {
	return &TeamService{db: db}
}

// TeamOverview is the aggregated statistics response for GET /team/overview.
type TeamOverview struct {
	ProjectCount int64 `json:"projectCount"`
	MemberCount  int64 `json:"memberCount"`
	VersionCount int64 `json:"versionCount"`
}

// GetOverview returns aggregated team stats for the given user.
func (s *TeamService) GetOverview(ctx context.Context, userID string) (*TeamOverview, error) {
	projectIDs, err := s.accessibleProjectIDs(userID)
	if err != nil {
		return nil, err
	}
	if len(projectIDs) == 0 {
		return &TeamOverview{}, nil
	}

	var projectCount, memberCount, versionCount int64
	s.db.WithContext(ctx).Model(&model.Project{}).
		Where("id IN ?", projectIDs).Count(&projectCount)
	s.db.WithContext(ctx).Model(&model.ProjectMember{}).
		Where("project_id IN ? AND status = 'active'", projectIDs).
		Distinct("user_id").Count(&memberCount)
	s.db.WithContext(ctx).Table("version_snapshots").
		Where("project_id IN ?", projectIDs).Count(&versionCount)

	return &TeamOverview{
		ProjectCount: projectCount,
		MemberCount:  memberCount,
		VersionCount: versionCount,
	}, nil
}

// MemberSummary is a compact member representation for team views.
type MemberSummary struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

// TeamProjectItem extends Project with team-page metadata.
type TeamProjectItem struct {
	model.Project
	VersionCount int64           `json:"versionCount"`
	Members      []MemberSummary `json:"members"`
}

// TeamProjectsParams holds filter and pagination options.
type TeamProjectsParams struct {
	UserID   string
	Keyword  string
	Type     string
	Page     int
	PageSize int
}

// ListProjects returns accessible projects with member summaries for the team page.
func (s *TeamService) ListProjects(ctx context.Context, params TeamProjectsParams) ([]TeamProjectItem, int64, error) {
	subQuery := s.db.Model(&model.ProjectMember{}).
		Select("project_id").
		Where("user_id = ? AND status = 'active'", params.UserID)

	q := s.db.WithContext(ctx).Model(&model.Project{}).
		Where("created_by = ? OR id IN (?)", params.UserID, subQuery)
	if params.Type != "" && params.Type != "all" {
		q = q.Where("type = ?", params.Type)
	}
	if params.Keyword != "" {
		q = q.Where("name ILIKE ?", "%"+params.Keyword+"%")
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var projects []model.Project
	offset := (params.Page - 1) * params.PageSize
	if err := q.Order("updated_at DESC").Offset(offset).Limit(params.PageSize).Find(&projects).Error; err != nil {
		return nil, 0, err
	}
	if len(projects) == 0 {
		return []TeamProjectItem{}, total, nil
	}

	projectIDs := make([]string, len(projects))
	for i, p := range projects {
		projectIDs[i] = p.ID
	}

	// Bulk-fetch member summaries.
	type memberRow struct {
		ProjectID string
		UserID    string
		Name      string
		Role      string
	}
	var memberRows []memberRow
	s.db.WithContext(ctx).
		Table("project_members pm").
		Select("pm.project_id, pm.user_id, COALESCE(u.display_name, u.username) AS name, pm.role").
		Joins("LEFT JOIN users u ON u.id = pm.user_id").
		Where("pm.project_id IN ? AND pm.status = 'active'", projectIDs).
		Scan(&memberRows)

	membersByProject := make(map[string][]MemberSummary, len(projects))
	for _, r := range memberRows {
		membersByProject[r.ProjectID] = append(membersByProject[r.ProjectID], MemberSummary{
			UserID: r.UserID,
			Name:   r.Name,
			Role:   r.Role,
		})
	}

	// Bulk-fetch version counts.
	type versionCountRow struct {
		ProjectID string
		Count     int64
	}
	var vcRows []versionCountRow
	s.db.WithContext(ctx).
		Table("version_snapshots").
		Select("project_id, COUNT(*) AS count").
		Where("project_id IN ?", projectIDs).
		Group("project_id").
		Scan(&vcRows)
	vcMap := make(map[string]int64, len(vcRows))
	for _, vc := range vcRows {
		vcMap[vc.ProjectID] = vc.Count
	}

	items := make([]TeamProjectItem, len(projects))
	for i, p := range projects {
		members := membersByProject[p.ID]
		if members == nil {
			members = []MemberSummary{}
		}
		items[i] = TeamProjectItem{
			Project:      p,
			VersionCount: vcMap[p.ID],
			Members:      members,
		}
	}
	return items, total, nil
}

// OperatorSummary is a compact user representation for activity events.
type OperatorSummary struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// ActivityItem represents a version-creation event on the team timeline.
type ActivityItem struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	ProjectID   string          `json:"projectId"`
	ProjectType string          `json:"projectType"`
	ProjectName string          `json:"projectName"`
	VersionID   string          `json:"versionId"`
	Label       string          `json:"label"`
	Operator    OperatorSummary `json:"operator"`
	CreatedAt   time.Time       `json:"createdAt"`
}

// ListActivities returns recent version-creation events visible to the user.
func (s *TeamService) ListActivities(ctx context.Context, userID string, limit int) ([]ActivityItem, error) {
	if limit <= 0 {
		limit = 20
	}
	projectIDs, err := s.accessibleProjectIDs(userID)
	if err != nil {
		return nil, err
	}
	if len(projectIDs) == 0 {
		return []ActivityItem{}, nil
	}

	type activityRow struct {
		ID          string
		ProjectID   string
		ProjectType string
		ProjectName string
		Label       string
		CreatedAt   time.Time
		CreatedBy   string
		DisplayName string
	}
	var rows []activityRow
	err = s.db.WithContext(ctx).
		Table("version_snapshots vs").
		Select("vs.id, vs.project_id, vs.project_type, p.name AS project_name, vs.label, vs.created_at, vs.created_by, COALESCE(u.display_name, u.username) AS display_name").
		Joins("LEFT JOIN projects p ON p.id = vs.project_id").
		Joins("LEFT JOIN users u ON u.id = vs.created_by").
		Where("vs.project_id IN ?", projectIDs).
		Order("vs.created_at DESC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	items := make([]ActivityItem, len(rows))
	for i, r := range rows {
		items[i] = ActivityItem{
			ID:          "activity_" + r.ID,
			Type:        "version_created",
			ProjectID:   r.ProjectID,
			ProjectType: r.ProjectType,
			ProjectName: r.ProjectName,
			VersionID:   r.ID,
			Label:       r.Label,
			Operator: OperatorSummary{
				ID:          r.CreatedBy,
				DisplayName: r.DisplayName,
			},
			CreatedAt: r.CreatedAt,
		}
	}
	return items, nil
}

// accessibleProjectIDs returns all project IDs the user can access (owner or member).
func (s *TeamService) accessibleProjectIDs(userID string) ([]string, error) {
	var ids []string
	err := s.db.Raw(`
		SELECT id FROM projects WHERE created_by = ?
		UNION
		SELECT project_id FROM project_members WHERE user_id = ? AND status = 'active'
	`, userID, userID).Scan(&ids).Error
	return ids, err
}
