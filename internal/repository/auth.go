package repository

import (
	"errors"
	"time"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type AuthRepository struct {
	db *gorm.DB
}

func NewAuthRepository(db *gorm.DB) *AuthRepository {
	return &AuthRepository{db: db}
}

func (r *AuthRepository) SaveRefreshToken(rt *model.RefreshToken) error {
	return r.db.Create(rt).Error
}

func (r *AuthRepository) FindActiveRefreshToken(tokenHash string) (*model.RefreshToken, error) {
	var rt model.RefreshToken
	err := r.db.Where("token_hash = ? AND is_revoked = false AND expires_at > ?", tokenHash, time.Now()).
		First(&rt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &rt, err
}

func (r *AuthRepository) RevokeRefreshToken(tokenHash string) error {
	return r.db.Model(&model.RefreshToken{}).
		Where("token_hash = ?", tokenHash).
		Update("is_revoked", true).Error
}

func (r *AuthRepository) RevokeAllByUser(userID string) error {
	return r.db.Model(&model.RefreshToken{}).
		Where("user_id = ? AND is_revoked = false", userID).
		Update("is_revoked", true).Error
}
