package service

import (
	"context"
	"errors"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"
	jwtpkg "optitree-backend/pkg/jwt"

	"github.com/redis/go-redis/v9"
)

var (
	ErrUserNotFound      = errors.New("用户不存在")
	ErrWrongPassword     = errors.New("密码错误")
	ErrUsernameTaken     = errors.New("用户名已被占用")
	ErrEmailTaken        = errors.New("邮箱已被注册")
	ErrTokenInvalid      = errors.New("token无效")
	ErrResetTokenInvalid = errors.New("重置链接已失效或不存在")
)

type AuthService struct {
	userRepo   *repository.UserRepository
	authRepo   *repository.AuthRepository
	jwtManager *jwtpkg.Manager
	rdb        *redis.Client
}

func NewAuthService(
	userRepo *repository.UserRepository,
	authRepo *repository.AuthRepository,
	jwtManager *jwtpkg.Manager,
	rdb *redis.Client,
) *AuthService {
	return &AuthService{
		userRepo:   userRepo,
		authRepo:   authRepo,
		jwtManager: jwtManager,
		rdb:        rdb,
	}
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

type LoginResult struct {
	User         *model.User
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int64
}

// Register 用户注册
func (s *AuthService) Register(ctx context.Context, input RegisterInput) (*model.User, error) {
	// 检查用户名唯一性
	exists, err := s.userRepo.ExistsByUsername(input.Username)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrUsernameTaken
	}

	// 检查邮箱唯一性
	exists, err = s.userRepo.ExistsByEmail(input.Email)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrEmailTaken
	}

	// 哈希密码
	hash, err := util.HashPassword(input.Password)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		ID:           util.NewUserID(),
		Username:     input.Username,
		DisplayName:  input.Username,
		Email:        input.Email,
		PasswordHash: hash,
		Role:         constant.SystemRoleUser,
		Status:       constant.UserStatusActive,
	}
	if err := s.userRepo.Create(user); err != nil {
		return nil, err
	}
	return user, nil
}

// Login 用户登录
func (s *AuthService) Login(ctx context.Context, usernameOrEmail, password string, remember bool, ip, userAgent string) (*LoginResult, error) {
	user, err := s.userRepo.FindByUsernameOrEmail(usernameOrEmail)
	if err != nil {
		return nil, err
	}

	// 写登录日志（无论成功失败）
	logRecord := &model.LoginLog{
		ID:         util.NewID("log"),
		UserID:     "",
		IPAddress:  nil,
		DeviceInfo: userAgent,
	}

	if user == nil || !util.CheckPassword(password, user.PasswordHash) {
		logRecord.Success = false
		logRecord.FailReason = "用户名或密码错误"
		if user != nil {
			logRecord.UserID = user.ID
		}
		_ = s.userRepo.SaveLoginLog(logRecord)
		return nil, ErrWrongPassword
	}

	logRecord.UserID = user.ID
	logRecord.Success = true
	_ = s.userRepo.SaveLoginLog(logRecord)

	// 更新最后登录时间
	now := time.Now()
	_ = s.userRepo.UpdateFields(user.ID, map[string]interface{}{"last_login_at": now})

	return s.generateTokens(ctx, user, remember)
}

func (s *AuthService) generateTokens(ctx context.Context, user *model.User, remember bool) (*LoginResult, error) {
	accessToken, jti, err := s.jwtManager.GenerateAccessToken(user.ID, user.Role)
	if err != nil {
		return nil, err
	}

	refreshTokenStr, expiresAt, err := s.jwtManager.GenerateRefreshToken(user.ID, remember)
	if err != nil {
		return nil, err
	}

	// 缓存 AccessToken 到 Redis
	accessKey := constant.RedisKeyAccessToken + accessToken
	_ = s.rdb.HSet(ctx, accessKey, map[string]interface{}{
		"userId":    user.ID,
		"expiresAt": expiresAt.Unix(),
		"jti":       jti,
	})
	_ = s.rdb.Expire(ctx, accessKey, 2*time.Hour)

	// 保存 RefreshToken 到数据库
	rt := &model.RefreshToken{
		ID:        util.NewID("rt"),
		UserID:    user.ID,
		TokenHash: util.HashToken(refreshTokenStr),
		ExpiresAt: expiresAt,
	}
	if err := s.authRepo.SaveRefreshToken(rt); err != nil {
		return nil, err
	}

	return &LoginResult{
		User:         user,
		AccessToken:  accessToken,
		RefreshToken: refreshTokenStr,
		TokenType:    "Bearer",
		ExpiresIn:    int64(2 * time.Hour / time.Second),
	}, nil
}

// RefreshToken 刷新 AccessToken
func (s *AuthService) RefreshToken(ctx context.Context, refreshTokenStr string) (*LoginResult, error) {
	tokenHash := util.HashToken(refreshTokenStr)
	rt, err := s.authRepo.FindActiveRefreshToken(tokenHash)
	if err != nil {
		return nil, err
	}
	if rt == nil {
		return nil, ErrTokenInvalid
	}

	user, err := s.userRepo.FindByID(rt.UserID)
	if err != nil || user == nil {
		return nil, ErrUserNotFound
	}

	return s.generateTokens(ctx, user, false)
}

// Logout 退出登录
func (s *AuthService) Logout(ctx context.Context, jti, refreshTokenStr string) error {
	// 将 AccessToken JTI 加入黑名单，TTL=2h
	blacklistKey := constant.RedisKeyBlacklist + jti
	_ = s.rdb.Set(ctx, blacklistKey, "1", 2*time.Hour)

	// 撤销 RefreshToken
	if refreshTokenStr != "" {
		tokenHash := util.HashToken(refreshTokenStr)
		_ = s.authRepo.RevokeRefreshToken(tokenHash)
	}
	return nil
}

// ChangePassword 修改密码
func (s *AuthService) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	user, err := s.userRepo.FindByID(userID)
	if err != nil || user == nil {
		return ErrUserNotFound
	}

	if !util.CheckPassword(oldPassword, user.PasswordHash) {
		return ErrWrongPassword
	}

	hash, err := util.HashPassword(newPassword)
	if err != nil {
		return err
	}

	if err := s.userRepo.UpdateFields(userID, map[string]interface{}{"password_hash": hash}); err != nil {
		return err
	}

	// 吊销该用户所有 RefreshToken
	return s.authRepo.RevokeAllByUser(userID)
}

// ForgotPassword 发起密码重置（生成 token，实际发邮件留 P1 实现）
func (s *AuthService) ForgotPassword(ctx context.Context, email string) (string, error) {
	user, err := s.userRepo.FindByEmail(email)
	if err != nil {
		return "", err
	}
	// 不暴露用户是否存在
	if user == nil {
		return "", nil
	}

	token, err := util.RandomToken(32)
	if err != nil {
		return "", err
	}

	resetKey := constant.RedisKeyResetPassword + token
	_ = s.rdb.Set(ctx, resetKey, user.ID, 15*time.Minute)

	return token, nil
}

// ResetPassword 使用重置 token 重置密码
func (s *AuthService) ResetPassword(ctx context.Context, token, newPassword string) error {
	resetKey := constant.RedisKeyResetPassword + token
	userID, err := s.rdb.Get(ctx, resetKey).Result()
	if err != nil {
		return ErrResetTokenInvalid
	}

	hash, err := util.HashPassword(newPassword)
	if err != nil {
		return err
	}

	if err := s.userRepo.UpdateFields(userID, map[string]interface{}{"password_hash": hash}); err != nil {
		return err
	}

	_ = s.rdb.Del(ctx, resetKey)
	return nil
}
