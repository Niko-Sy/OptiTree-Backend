package service

import (
	"context"
	"errors"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"
)

var (
	ErrMemberNotFound        = errors.New("成员不存在")
	ErrAlreadyMember         = errors.New("用户已是项目成员")
	ErrCannotRemoveLastAdmin = errors.New("不能移除最后一个管理员")
	ErrInvalidRole           = errors.New("无效的角色")
)

type MemberService struct {
	memberRepo  *repository.MemberRepository
	projectRepo *repository.ProjectRepository
	userRepo    *repository.UserRepository
}

func NewMemberService(
	memberRepo *repository.MemberRepository,
	projectRepo *repository.ProjectRepository,
	userRepo *repository.UserRepository,
) *MemberService {
	return &MemberService{memberRepo: memberRepo, projectRepo: projectRepo, userRepo: userRepo}
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

func (s *MemberService) InviteMember(ctx context.Context, input InviteMemberInput) (*model.Invitation, error) {
	if !isValidRole(input.Role) {
		return nil, ErrInvalidRole
	}

	// 检查用户是否已存在并已是成员
	user, _ := s.userRepo.FindByEmail(input.Email)
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

	invitation := &model.Invitation{
		ID:        util.NewInviteID(),
		ProjectID: input.ProjectID,
		Email:     input.Email,
		Role:      input.Role,
		Status:    constant.InviteStatusPending,
		Token:     token,
		InvitedBy: input.InvitedBy,
		ExpiresAt: time.Now().Add(72 * time.Hour),
	}

	// P0 暂不实际发送邮件，P1 接入 SMTP 时在此处调用邮件服务
	// 此处仅返回邀请记录
	_ = invitation
	return invitation, nil
}

func (s *MemberService) UpdateRole(ctx context.Context, projectID, memberID, newRole string) (*model.ProjectMember, error) {
	if !isValidRole(newRole) {
		return nil, ErrInvalidRole
	}

	member, err := s.memberRepo.FindByID(memberID)
	if err != nil {
		return nil, err
	}
	if member == nil || member.ProjectID != projectID {
		return nil, ErrMemberNotFound
	}

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
	return member, nil
}

func (s *MemberService) RemoveMember(ctx context.Context, projectID, memberID string) error {
	member, err := s.memberRepo.FindByID(memberID)
	if err != nil {
		return err
	}
	if member == nil || member.ProjectID != projectID {
		return ErrMemberNotFound
	}

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
	return nil
}

func isValidRole(role string) bool {
	return role == constant.RoleAdmin || role == constant.RoleEditor || role == constant.RoleViewer
}
