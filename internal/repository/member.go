package repository

import (
	"errors"
	"strings"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemberRepository struct {
	db *gorm.DB
}

func NewMemberRepository(db *gorm.DB) *MemberRepository {
	return &MemberRepository{db: db}
}

func (r *MemberRepository) useTx(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.db
}

func (r *MemberRepository) Create(member *model.ProjectMember) error {
	return r.CreateWithTx(nil, member)
}

func (r *MemberRepository) CreateWithTx(tx *gorm.DB, member *model.ProjectMember) error {
	return r.useTx(tx).Create(member).Error
}

func (r *MemberRepository) FindByProjectAndUser(projectID, userID string) (*model.ProjectMember, error) {
	return r.FindByProjectAndUserWithTx(nil, projectID, userID)
}

func (r *MemberRepository) FindByProjectAndUserWithTx(tx *gorm.DB, projectID, userID string) (*model.ProjectMember, error) {
	var member model.ProjectMember
	err := r.useTx(tx).Where("project_id = ? AND user_id = ? AND status = 'active'", projectID, userID).
		First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &member, err
}

func (r *MemberRepository) FindByProject(projectID string) ([]model.ProjectMember, error) {
	var members []model.ProjectMember
	err := r.db.Where("project_id = ? AND status = 'active'", projectID).
		Preload("User").
		Find(&members).Error
	return members, err
}

func (r *MemberRepository) FindByID(id string) (*model.ProjectMember, error) {
	var member model.ProjectMember
	err := r.db.Where("id = ?", id).First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &member, err
}

func (r *MemberRepository) Update(member *model.ProjectMember) error {
	return r.db.Save(member).Error
}

func (r *MemberRepository) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.ProjectMember{}).Error
}

func (r *MemberRepository) DeleteByProject(tx *gorm.DB, projectID string) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Where("project_id = ?", projectID).Delete(&model.ProjectMember{}).Error
}

func (r *MemberRepository) CountAdmins(projectID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ProjectMember{}).
		Where("project_id = ? AND role = 'admin' AND status = 'active'", projectID).
		Count(&count).Error
	return count, err
}

func (r *MemberRepository) CountByProject(projectID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.ProjectMember{}).
		Where("project_id = ? AND status = 'active'", projectID).
		Count(&count).Error
	return count, err
}

func (r *MemberRepository) IncrMemberCount(projectID string, delta int) error {
	return r.IncrMemberCountWithTx(nil, projectID, delta)
}

func (r *MemberRepository) IncrMemberCountWithTx(tx *gorm.DB, projectID string, delta int) error {
	return r.useTx(tx).Model(&model.Project{}).Where("id = ?", projectID).
		UpdateColumn("member_count", gorm.Expr("member_count + ?", delta)).Error
}

func (r *MemberRepository) CreateInvitation(tx *gorm.DB, invitation *model.Invitation) error {
	return r.useTx(tx).Create(invitation).Error
}

func (r *MemberRepository) UpdateInvitation(tx *gorm.DB, invitation *model.Invitation) error {
	return r.useTx(tx).Save(invitation).Error
}

func (r *MemberRepository) FindInvitationByID(id string) (*model.Invitation, error) {
	var invitation model.Invitation
	err := r.db.Where("id = ?", id).First(&invitation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &invitation, err
}

func (r *MemberRepository) FindInvitationByIDForUpdate(tx *gorm.DB, id string) (*model.Invitation, error) {
	var invitation model.Invitation
	err := r.useTx(tx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).
		First(&invitation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &invitation, err
}

func (r *MemberRepository) FindPendingInvitationByProjectAndEmail(projectID, email string) (*model.Invitation, error) {
	return r.FindPendingInvitationByProjectAndEmailForUpdate(nil, projectID, email)
}

func (r *MemberRepository) FindPendingInvitationByProjectAndEmailForUpdate(tx *gorm.DB, projectID, email string) (*model.Invitation, error) {
	var invitation model.Invitation
	db := r.useTx(tx).Where("project_id = ? AND email = ? AND status = ?", projectID, email, "pending")
	if tx != nil {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := db.First(&invitation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &invitation, err
}

func (r *MemberRepository) ListInvitationsByProject(projectID, status string, page, pageSize int) ([]model.Invitation, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	q := r.db.Model(&model.Invitation{}).Where("project_id = ?", projectID)
	if status != "" && strings.ToLower(status) != "all" {
		q = q.Where("status = ?", status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var invitations []model.Invitation
	offset := (page - 1) * pageSize
	err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&invitations).Error
	if err != nil {
		return nil, 0, err
	}
	return invitations, total, nil
}

func (r *MemberRepository) ListInvitationsByEmail(email, status string, page, pageSize int) ([]model.Invitation, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	q := r.db.Model(&model.Invitation{}).Where("email = ?", email)
	if status != "" && strings.ToLower(status) != "all" {
		q = q.Where("status = ?", status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var invitations []model.Invitation
	offset := (page - 1) * pageSize
	err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&invitations).Error
	if err != nil {
		return nil, 0, err
	}
	return invitations, total, nil
}
