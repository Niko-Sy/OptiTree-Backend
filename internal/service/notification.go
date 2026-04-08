package service

import (
	"context"
	"errors"

	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
)

var ErrNotificationNotFound = errors.New("通知不存在")

type NotificationService struct {
	notificationRepo *repository.NotificationRepository
}

func NewNotificationService(notificationRepo *repository.NotificationRepository) *NotificationService {
	return &NotificationService{notificationRepo: notificationRepo}
}

type ListNotificationsParams struct {
	UserID   string
	IsRead   *bool
	Type     string
	Page     int
	PageSize int
}

func (s *NotificationService) List(ctx context.Context, params ListNotificationsParams) ([]model.Notification, int64, error) {
	return s.notificationRepo.ListByUser(params.UserID, params.IsRead, params.Type, params.Page, params.PageSize)
}

func (s *NotificationService) CountUnread(ctx context.Context, userID string) (int64, error) {
	return s.notificationRepo.CountUnread(userID)
}

func (s *NotificationService) MarkRead(ctx context.Context, userID, notificationID string) error {
	affected, err := s.notificationRepo.MarkRead(notificationID, userID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotificationNotFound
	}
	return nil
}

func (s *NotificationService) MarkAllRead(ctx context.Context, userID string) error {
	_, err := s.notificationRepo.MarkAllRead(userID)
	return err
}

func (s *NotificationService) GetByID(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	n, err := s.notificationRepo.FindByIDAndUser(notificationID, userID)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNotificationNotFound
	}
	return n, nil
}
