package repository

import (
	"errors"
	"strings"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type NotificationRepository struct {
	db *gorm.DB
}

func NewNotificationRepository(db *gorm.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

func (r *NotificationRepository) useTx(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.db
}

func (r *NotificationRepository) Create(tx *gorm.DB, notification *model.Notification) error {
	return r.useTx(tx).Create(notification).Error
}

func (r *NotificationRepository) ListByUser(userID string, isRead *bool, notifType string, page, pageSize int) ([]model.Notification, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	q := r.db.Model(&model.Notification{}).Where("user_id = ?", userID)
	if isRead != nil {
		q = q.Where("is_read = ?", *isRead)
	}
	if notifType != "" {
		q = q.Where("type = ?", notifType)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.Notification
	offset := (page - 1) * pageSize
	err := q.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *NotificationRepository) CountUnread(userID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.Notification{}).
		Where("user_id = ? AND is_read = false", userID).
		Count(&count).Error
	return count, err
}

func (r *NotificationRepository) FindByIDAndUser(id, userID string) (*model.Notification, error) {
	var n model.Notification
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &n, err
}

func (r *NotificationRepository) MarkRead(id, userID string) (int64, error) {
	result := r.db.Model(&model.Notification{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("is_read", true)
	return result.RowsAffected, result.Error
}

func (r *NotificationRepository) MarkAllRead(userID string) (int64, error) {
	result := r.db.Model(&model.Notification{}).
		Where("user_id = ? AND is_read = false", userID).
		Update("is_read", true)
	return result.RowsAffected, result.Error
}

type AuditLogRepository struct {
	db *gorm.DB
}

func NewAuditLogRepository(db *gorm.DB) *AuditLogRepository {
	return &AuditLogRepository{db: db}
}

func (r *AuditLogRepository) useTx(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.db
}

func (r *AuditLogRepository) Create(tx *gorm.DB, log *model.AuditLog) error {
	if log == nil {
		return nil
	}

	if log.IPAddress != nil && strings.TrimSpace(*log.IPAddress) == "" {
		log.IPAddress = nil
	}
	if log.UserAgent != nil && strings.TrimSpace(*log.UserAgent) == "" {
		log.UserAgent = nil
	}

	db := r.useTx(tx)
	if log.IPAddress == nil {
		db = db.Omit("IPAddress")
	}
	if log.UserAgent == nil {
		db = db.Omit("UserAgent")
	}
	return db.Create(log).Error
}
