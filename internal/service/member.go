package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	ErrMemberNotFound          = errors.New("成员不存在")
	ErrAlreadyMember           = errors.New("用户已是项目成员")
	ErrCannotRemoveLastAdmin   = errors.New("不能移除最后一个管理员")
	ErrInvalidRole             = errors.New("无效的角色")
	ErrMemberProjectNotFound   = errors.New("项目不存在")
	ErrInvitationNotFound      = errors.New("邀请不存在")
	ErrInvitationExpired       = errors.New("邀请已过期")
	ErrInvitationProcessed     = errors.New("邀请已处理")
	ErrInvitationEmailMismatch = errors.New("邀请邮箱与当前用户不一致")
	ErrInviteEmailRequired     = errors.New("邀请邮箱不能为空")
)

type MemberService struct {
	db               *gorm.DB
	memberRepo       *repository.MemberRepository
	projectRepo      *repository.ProjectRepository
	userRepo         *repository.UserRepository
	notificationRepo *repository.NotificationRepository
	auditRepo        *repository.AuditLogRepository
}

func NewMemberService(
	db *gorm.DB,
	memberRepo *repository.MemberRepository,
	projectRepo *repository.ProjectRepository,
	userRepo *repository.UserRepository,
	notificationRepo *repository.NotificationRepository,
	auditRepo *repository.AuditLogRepository,
) *MemberService {
	return &MemberService{
		db:               db,
		memberRepo:       memberRepo,
		projectRepo:      projectRepo,
		userRepo:         userRepo,
		notificationRepo: notificationRepo,
		auditRepo:        auditRepo,
	}
}

func (s *MemberService) ListMembers(ctx context.Context, projectID string) ([]model.ProjectMember, error) {
	return s.memberRepo.FindByProject(projectID)
}

type InviteMemberInput struct {
	ProjectID string
	Email     string
	Role      string
	InvitedBy string
}

type InviteCandidate struct {
	Email                string     `json:"email"`
	Registered           bool       `json:"registered"`
	UserID               string     `json:"userId,omitempty"`
	DisplayName          string     `json:"displayName,omitempty"`
	AlreadyMember        bool       `json:"alreadyMember"`
	HasPendingInvitation bool       `json:"hasPendingInvitation"`
	PendingInvitationID  string     `json:"pendingInvitationId,omitempty"`
	PendingExpiresAt     *time.Time `json:"pendingExpiresAt,omitempty"`
}

func (s *MemberService) InviteMember(ctx context.Context, input InviteMemberInput) (*model.Invitation, error) {
	if !isValidRole(input.Role) {
		return nil, ErrInvalidRole
	}
	email := normalizeEmail(input.Email)
	if email == "" {
		return nil, ErrInviteEmailRequired
	}

	project, err := s.projectRepo.FindByID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrMemberProjectNotFound
	}

	inviter, err := s.userRepo.FindByID(input.InvitedBy)
	if err != nil {
		return nil, err
	}
	if inviter == nil {
		return nil, ErrUserNotFound
	}

	// 检查用户是否已存在并已是成员
	user, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return nil, err
	}
	if user != nil {
		existing, err := s.memberRepo.FindByProjectAndUser(input.ProjectID, user.ID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, ErrAlreadyMember
		}
	}

	token, err := util.RandomToken(32)
	if err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(72 * time.Hour)
	inviterName := displayName(inviter)

	var invitation *model.Invitation
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		pending, err := s.memberRepo.FindPendingInvitationByProjectAndEmailForUpdate(tx, input.ProjectID, email)
		if err != nil {
			return err
		}

		if pending != nil {
			pending.Role = input.Role
			pending.InvitedBy = input.InvitedBy
			pending.Token = token
			pending.ExpiresAt = expiresAt
			pending.Status = constant.InviteStatusPending
			if err := s.memberRepo.UpdateInvitation(tx, pending); err != nil {
				return err
			}
			invitation = pending
		} else {
			invitation = &model.Invitation{
				ID:        util.NewInviteID(),
				ProjectID: input.ProjectID,
				Email:     email,
				Role:      input.Role,
				Status:    constant.InviteStatusPending,
				Token:     token,
				InvitedBy: input.InvitedBy,
				ExpiresAt: expiresAt,
			}
			if err := s.memberRepo.CreateInvitation(tx, invitation); err != nil {
				return err
			}
		}

		if user != nil {
			projectID := project.ID
			resourceID := invitation.ID
			notification := &model.Notification{
				ID:         util.NewNotificationID(),
				UserID:     user.ID,
				Type:       constant.NotificationTypeProjectInvite,
				Title:      fmt.Sprintf("%s 邀请你加入项目", inviterName),
				Content:    fmt.Sprintf("项目「%s」邀请你以 %s 身份加入", project.Name, input.Role),
				ProjectID:  &projectID,
				ResourceID: &resourceID,
				ExtraJson: mapToJSON(map[string]interface{}{
					"invitationId": invitation.ID,
					"projectId":    project.ID,
					"projectName":  project.Name,
					"role":         input.Role,
					"expiresAt":    invitation.ExpiresAt,
					"actions":      []string{"accept", "reject"},
				}),
			}
			if err := s.notificationRepo.Create(tx, notification); err != nil {
				return err
			}
		}

		operatorID := input.InvitedBy
		projectID := project.ID
		audit := &model.AuditLog{
			ID:           util.NewAuditLogID(),
			UserID:       &operatorID,
			OperatorName: inviterName,
			Action:       constant.AuditActionMemberInvite,
			ResourceType: "invitation",
			ResourceID:   invitation.ID,
			Summary:      fmt.Sprintf("邀请 %s 加入项目，角色：%s", email, input.Role),
			ProjectID:    &projectID,
		}
		return s.auditRepo.Create(tx, audit)
	})
	if err != nil {
		return nil, err
	}

	return invitation, nil
}

func (s *MemberService) ListInvitations(ctx context.Context, projectID, status string, page, pageSize int) ([]model.Invitation, int64, error) {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, 0, err
	}
	if project == nil {
		return nil, 0, ErrMemberProjectNotFound
	}
	return s.memberRepo.ListInvitationsByProject(projectID, status, page, pageSize)
}

func (s *MemberService) ListMyInvitations(ctx context.Context, userID, status string, page, pageSize int) ([]model.Invitation, int64, error) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return nil, 0, err
	}
	if user == nil {
		return nil, 0, ErrUserNotFound
	}

	return s.memberRepo.ListInvitationsByEmail(normalizeEmail(user.Email), status, page, pageSize)
}

func (s *MemberService) GetInviteCandidate(ctx context.Context, projectID, email string) (*InviteCandidate, error) {
	normalizedEmail := normalizeEmail(email)
	if normalizedEmail == "" {
		return nil, ErrInviteEmailRequired
	}

	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrMemberProjectNotFound
	}

	res := &InviteCandidate{Email: normalizedEmail}
	user, err := s.userRepo.FindByEmail(normalizedEmail)
	if err != nil {
		return nil, err
	}
	if user != nil {
		res.Registered = true
		res.UserID = user.ID
		res.DisplayName = displayName(user)

		member, err := s.memberRepo.FindByProjectAndUser(projectID, user.ID)
		if err != nil {
			return nil, err
		}
		res.AlreadyMember = member != nil
	}

	pending, err := s.memberRepo.FindPendingInvitationByProjectAndEmail(projectID, normalizedEmail)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		res.HasPendingInvitation = true
		res.PendingInvitationID = pending.ID
		res.PendingExpiresAt = &pending.ExpiresAt
	}

	return res, nil
}

func (s *MemberService) AcceptInvitation(ctx context.Context, projectID, invitationID, userID string) (*model.ProjectMember, error) {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrMemberProjectNotFound
	}

	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	userEmail := normalizeEmail(user.Email)
	var member *model.ProjectMember

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		invitation, err := s.memberRepo.FindInvitationByIDForUpdate(tx, invitationID)
		if err != nil {
			return err
		}
		if invitation == nil || invitation.ProjectID != projectID {
			return ErrInvitationNotFound
		}

		switch invitation.Status {
		case constant.InviteStatusAccepted:
			existing, err := s.memberRepo.FindByProjectAndUserWithTx(tx, projectID, userID)
			if err != nil {
				return err
			}
			if existing != nil {
				member = existing
				return nil
			}
			return ErrInvitationProcessed
		case constant.InviteStatusRejected, constant.InviteStatusExpired:
			return ErrInvitationProcessed
		}

		if time.Now().After(invitation.ExpiresAt) {
			invitation.Status = constant.InviteStatusExpired
			if err := s.memberRepo.UpdateInvitation(tx, invitation); err != nil {
				return err
			}
			return ErrInvitationExpired
		}

		if normalizeEmail(invitation.Email) != userEmail {
			return ErrInvitationEmailMismatch
		}

		existing, err := s.memberRepo.FindByProjectAndUserWithTx(tx, projectID, userID)
		if err != nil {
			return err
		}
		if existing != nil {
			invitation.Status = constant.InviteStatusAccepted
			if err := s.memberRepo.UpdateInvitation(tx, invitation); err != nil {
				return err
			}
			member = existing
			return nil
		}

		if !isValidRole(invitation.Role) {
			return ErrInvalidRole
		}

		newMember := &model.ProjectMember{
			ID:        util.NewMemberID(),
			ProjectID: projectID,
			UserID:    userID,
			Role:      invitation.Role,
			Status:    constant.MemberStatusActive,
		}
		if err := s.memberRepo.CreateWithTx(tx, newMember); err != nil {
			return err
		}
		if err := s.memberRepo.IncrMemberCountWithTx(tx, projectID, 1); err != nil {
			return err
		}

		invitation.Status = constant.InviteStatusAccepted
		if err := s.memberRepo.UpdateInvitation(tx, invitation); err != nil {
			return err
		}

		if err := s.createInviteDecisionSideEffects(tx, project, invitation, user, true); err != nil {
			return err
		}

		member = newMember
		return nil
	})
	if err != nil {
		return nil, err
	}

	return member, nil
}

func (s *MemberService) RejectInvitation(ctx context.Context, projectID, invitationID, userID string) error {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return err
	}
	if project == nil {
		return ErrMemberProjectNotFound
	}

	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}

	userEmail := normalizeEmail(user.Email)

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		invitation, err := s.memberRepo.FindInvitationByIDForUpdate(tx, invitationID)
		if err != nil {
			return err
		}
		if invitation == nil || invitation.ProjectID != projectID {
			return ErrInvitationNotFound
		}

		switch invitation.Status {
		case constant.InviteStatusRejected:
			return nil
		case constant.InviteStatusAccepted:
			return ErrInvitationProcessed
		case constant.InviteStatusExpired:
			return ErrInvitationExpired
		}

		if time.Now().After(invitation.ExpiresAt) {
			invitation.Status = constant.InviteStatusExpired
			if err := s.memberRepo.UpdateInvitation(tx, invitation); err != nil {
				return err
			}
			return ErrInvitationExpired
		}

		if normalizeEmail(invitation.Email) != userEmail {
			return ErrInvitationEmailMismatch
		}

		invitation.Status = constant.InviteStatusRejected
		if err := s.memberRepo.UpdateInvitation(tx, invitation); err != nil {
			return err
		}

		return s.createInviteDecisionSideEffects(tx, project, invitation, user, false)
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *MemberService) RevokeInvitation(ctx context.Context, projectID, invitationID, operatorID string) error {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return err
	}
	if project == nil {
		return ErrMemberProjectNotFound
	}

	operator, err := s.userRepo.FindByID(operatorID)
	if err != nil {
		return err
	}
	if operator == nil {
		return ErrUserNotFound
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		invitation, err := s.memberRepo.FindInvitationByIDForUpdate(tx, invitationID)
		if err != nil {
			return err
		}
		if invitation == nil || invitation.ProjectID != projectID {
			return ErrInvitationNotFound
		}

		switch invitation.Status {
		case constant.InviteStatusAccepted:
			return ErrInvitationProcessed
		case constant.InviteStatusExpired:
			return ErrInvitationExpired
		case constant.InviteStatusRejected:
			return nil
		}

		if time.Now().After(invitation.ExpiresAt) {
			invitation.Status = constant.InviteStatusExpired
			if err := s.memberRepo.UpdateInvitation(tx, invitation); err != nil {
				return err
			}
			return ErrInvitationExpired
		}

		invitation.Status = constant.InviteStatusRejected
		if err := s.memberRepo.UpdateInvitation(tx, invitation); err != nil {
			return err
		}

		if s.notificationRepo != nil {
			invitedUser, err := s.userRepo.FindByEmail(normalizeEmail(invitation.Email))
			if err != nil {
				return err
			}
			if invitedUser != nil {
				projectIDVal := project.ID
				resourceID := invitation.ID
				notif := &model.Notification{
					ID:         util.NewNotificationID(),
					UserID:     invitedUser.ID,
					Type:       constant.NotificationTypeProjectInvite,
					Title:      "项目邀请已撤销",
					Content:    fmt.Sprintf("你收到的项目「%s」邀请已被撤销", project.Name),
					ProjectID:  &projectIDVal,
					ResourceID: &resourceID,
					ExtraJson: mapToJSON(map[string]interface{}{
						"invitationId": invitation.ID,
						"projectId":    project.ID,
						"projectName":  project.Name,
						"action":       "revoke",
						"actions":      []string{},
					}),
				}
				if err := s.notificationRepo.Create(tx, notif); err != nil {
					return err
				}
			}
		}

		if s.auditRepo != nil {
			projectIDVal := project.ID
			opID := operator.ID
			audit := &model.AuditLog{
				ID:           util.NewAuditLogID(),
				UserID:       &opID,
				OperatorName: displayName(operator),
				Action:       constant.AuditActionMemberInviteRevoke,
				ResourceType: "invitation",
				ResourceID:   invitation.ID,
				Summary:      fmt.Sprintf("撤销了对 %s 的项目邀请", invitation.Email),
				ProjectID:    &projectIDVal,
			}
			if err := s.auditRepo.Create(tx, audit); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *MemberService) UpdateRole(ctx context.Context, projectID, memberID, newRole, operatorID string) (*model.ProjectMember, error) {
	if !isValidRole(newRole) {
		return nil, ErrInvalidRole
	}

	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrMemberProjectNotFound
	}

	member, err := s.memberRepo.FindByID(memberID)
	if err != nil {
		return nil, err
	}
	if member == nil || member.ProjectID != projectID {
		return nil, ErrMemberNotFound
	}
	originalRole := member.Role

	// 降级最后一个 admin 前检查
	if member.Role == constant.RoleAdmin && newRole != constant.RoleAdmin {
		count, err := s.memberRepo.CountAdmins(projectID)
		if err != nil {
			return nil, err
		}
		if count <= 1 {
			return nil, ErrCannotRemoveLastAdmin
		}
	}

	member.Role = newRole
	if err := s.memberRepo.Update(member); err != nil {
		return nil, err
	}

	if originalRole != newRole {
		operatorName := "系统"
		if operator, err := s.userRepo.FindByID(operatorID); err == nil && operator != nil {
			operatorName = displayName(operator)
		}

		if s.notificationRepo != nil {
			projectIDVal := project.ID
			memberIDVal := member.ID
			_ = s.notificationRepo.Create(nil, &model.Notification{
				ID:         util.NewNotificationID(),
				UserID:     member.UserID,
				Type:       constant.NotificationTypeMemberRoleChanged,
				Title:      "项目角色已更新",
				Content:    fmt.Sprintf("你在项目「%s」中的角色已从 %s 调整为 %s", project.Name, originalRole, newRole),
				ProjectID:  &projectIDVal,
				ResourceID: &memberIDVal,
				ExtraJson: mapToJSON(map[string]interface{}{
					"oldRole": originalRole,
					"newRole": newRole,
				}),
			})
		}

		if s.auditRepo != nil {
			projectIDVal := project.ID
			var auditUserID *string
			if operatorID != "" {
				auditUserID = &operatorID
			}
			_ = s.auditRepo.Create(nil, &model.AuditLog{
				ID:           util.NewAuditLogID(),
				UserID:       auditUserID,
				OperatorName: operatorName,
				Action:       constant.AuditActionMemberRoleUpdate,
				ResourceType: "member",
				ResourceID:   member.ID,
				Summary:      fmt.Sprintf("成员 %s 角色从 %s 调整为 %s", member.UserID, originalRole, newRole),
				ProjectID:    &projectIDVal,
			})
		}
	}

	return member, nil
}

func (s *MemberService) RemoveMember(ctx context.Context, projectID, memberID, operatorID string) error {
	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return err
	}
	if project == nil {
		return ErrMemberProjectNotFound
	}

	member, err := s.memberRepo.FindByID(memberID)
	if err != nil {
		return err
	}
	if member == nil || member.ProjectID != projectID {
		return ErrMemberNotFound
	}
	targetUserID := member.UserID

	if member.Role == constant.RoleAdmin {
		count, err := s.memberRepo.CountAdmins(projectID)
		if err != nil {
			return err
		}
		if count <= 1 {
			return ErrCannotRemoveLastAdmin
		}
	}

	if err := s.memberRepo.Delete(memberID); err != nil {
		return err
	}
	_ = s.memberRepo.IncrMemberCount(projectID, -1)

	if s.notificationRepo != nil {
		projectIDVal := project.ID
		resourceID := member.ID
		_ = s.notificationRepo.Create(nil, &model.Notification{
			ID:         util.NewNotificationID(),
			UserID:     targetUserID,
			Type:       constant.NotificationTypeMemberRoleChanged,
			Title:      "你已被移出项目",
			Content:    fmt.Sprintf("你已被移出项目「%s」", project.Name),
			ProjectID:  &projectIDVal,
			ResourceID: &resourceID,
			ExtraJson:  mapToJSON(map[string]interface{}{"action": "removed"}),
		})
	}

	if s.auditRepo != nil {
		operatorName := "系统"
		if operator, err := s.userRepo.FindByID(operatorID); err == nil && operator != nil {
			operatorName = displayName(operator)
		}
		projectIDVal := project.ID
		var auditUserID *string
		if operatorID != "" {
			auditUserID = &operatorID
		}
		_ = s.auditRepo.Create(nil, &model.AuditLog{
			ID:           util.NewAuditLogID(),
			UserID:       auditUserID,
			OperatorName: operatorName,
			Action:       constant.AuditActionMemberRemove,
			ResourceType: "member",
			ResourceID:   member.ID,
			Summary:      fmt.Sprintf("移除成员 %s", targetUserID),
			ProjectID:    &projectIDVal,
		})
	}

	return nil
}

func (s *MemberService) createInviteDecisionSideEffects(
	tx *gorm.DB,
	project *model.Project,
	invitation *model.Invitation,
	actor *model.User,
	accepted bool,
) error {
	action := "reject"
	actionCN := "拒绝"
	auditAction := constant.AuditActionMemberInviteRejected
	if accepted {
		action = "accept"
		actionCN = "接受"
		auditAction = constant.AuditActionMemberInviteAccepted
	}

	projectID := project.ID
	resourceID := invitation.ID
	actorName := displayName(actor)
	actorID := actor.ID

	if s.notificationRepo != nil && invitation.InvitedBy != "" && invitation.InvitedBy != actor.ID {
		notif := &model.Notification{
			ID:         util.NewNotificationID(),
			UserID:     invitation.InvitedBy,
			Type:       constant.NotificationTypeProjectInvite,
			Title:      fmt.Sprintf("%s已处理你的项目邀请", actorName),
			Content:    fmt.Sprintf("%s 已%s项目「%s」邀请", actorName, actionCN, project.Name),
			ProjectID:  &projectID,
			ResourceID: &resourceID,
			ExtraJson: mapToJSON(map[string]interface{}{
				"invitationId": invitation.ID,
				"projectId":    project.ID,
				"projectName":  project.Name,
				"action":       action,
				"actorUserId":  actor.ID,
			}),
		}
		if err := s.notificationRepo.Create(tx, notif); err != nil {
			return err
		}
	}

	if s.auditRepo != nil {
		audit := &model.AuditLog{
			ID:           util.NewAuditLogID(),
			UserID:       &actorID,
			OperatorName: actorName,
			Action:       auditAction,
			ResourceType: "invitation",
			ResourceID:   invitation.ID,
			Summary:      fmt.Sprintf("%s了项目邀请（%s）", actionCN, invitation.Email),
			ProjectID:    &projectID,
		}
		if err := s.auditRepo.Create(tx, audit); err != nil {
			return err
		}
	}

	return nil
}

func displayName(user *model.User) string {
	if user == nil {
		return "系统"
	}
	if strings.TrimSpace(user.DisplayName) != "" {
		return user.DisplayName
	}
	if strings.TrimSpace(user.Username) != "" {
		return user.Username
	}
	return user.ID
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func mapToJSON(payload map[string]interface{}) datatypes.JSON {
	if len(payload) == 0 {
		return datatypes.JSON([]byte("{}"))
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return datatypes.JSON([]byte("{}"))
	}
	return datatypes.JSON(b)
}

func isValidRole(role string) bool {
	return role == constant.RoleAdmin || role == constant.RoleEditor || role == constant.RoleViewer
}
