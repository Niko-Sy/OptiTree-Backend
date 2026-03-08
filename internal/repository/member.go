package repository

import (
	"errors"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type MemberRepository struct {
	db *gorm.DB
}

func NewMemberRepository(db *gorm.DB) *MemberRepository {
	return &MemberRepository{db: db}
}

func (r *MemberRepository) Create(member *model.ProjectMember) error {
	return r.db.Create(member).Error
}

func (r *MemberRepository) FindByProjectAndUser(projectID, userID string) (*model.ProjectMember, error) {
	var member model.ProjectMember
	err := r.db.Where("project_id = ? AND user_id = ? AND status = 'active'", projectID, userID).
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
	return r.db.Model(&model.Project{}).Where("id = ?", projectID).
		UpdateColumn("member_count", gorm.Expr("member_count + ?", delta)).Error
}
