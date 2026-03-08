package service

import (
	"context"
	"errors"
	"mime/multipart"

	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
)

var ErrEmailInUse = errors.New("邮箱已被其他账号使用")

type UserService struct {
	userRepo       *repository.UserRepository
	storageService *StorageService
}

func NewUserService(userRepo *repository.UserRepository, storage *StorageService) *UserService {
	return &UserService{userRepo: userRepo, storageService: storage}
}

func (s *UserService) GetMe(ctx context.Context, userID string) (*model.User, error) {
	return s.userRepo.FindByID(userID)
}

type UpdateProfileInput struct {
	DisplayName string
	Email       string
}

func (s *UserService) UpdateProfile(ctx context.Context, userID string, input UpdateProfileInput) (*model.User, error) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil || user == nil {
		return nil, ErrUserNotFound
	}

	// 检查邮箱是否被其他人占用
	if input.Email != user.Email {
		exists, err := s.userRepo.ExistsByEmail(input.Email)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrEmailInUse
		}
	}

	user.DisplayName = input.DisplayName
	user.Email = input.Email
	if err := s.userRepo.Update(user); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserService) UploadAvatar(ctx context.Context, userID string, file multipart.File, header *multipart.FileHeader) (string, error) {
	url, err := s.storageService.SaveImage(file, header)
	if err != nil {
		return "", err
	}
	if err := s.userRepo.UpdateFields(userID, map[string]interface{}{"avatar": url}); err != nil {
		return "", err
	}
	return url, nil
}

func (s *UserService) GetLoginLogs(ctx context.Context, userID string, page, pageSize int) ([]model.LoginLog, int64, error) {
	return s.userRepo.GetLoginLogs(userID, page, pageSize)
}
