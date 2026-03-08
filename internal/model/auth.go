package model

import (
	"net"
	"time"
)

// RefreshToken 持久化的 Refresh Token 记录
type RefreshToken struct {
	ID        string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	UserID    string    `gorm:"column:user_id;size:32;not null;index" json:"userId"`
	TokenHash string    `gorm:"column:token_hash;uniqueIndex;not null" json:"tokenHash"`
	IsRevoked bool      `gorm:"column:is_revoked;default:false" json:"isRevoked"`
	UserAgent string    `gorm:"column:user_agent;size:500" json:"userAgent"`
	IPAddress net.IP    `gorm:"column:ip_address;type:inet" json:"ipAddress"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null" json:"expiresAt"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
}

func (RefreshToken) TableName() string {
	return "refresh_tokens"
}

// LoginLog 登录记录
type LoginLog struct {
	ID         string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	UserID     string    `gorm:"column:user_id;size:32;not null;index" json:"userId"`
	Success    bool      `gorm:"column:success;not null" json:"success"`
	IPAddress  net.IP    `gorm:"column:ip_address;type:inet" json:"ipAddress"`
	Region     string    `gorm:"column:region;size:100" json:"region"`
	DeviceInfo string    `gorm:"column:device_info;size:500" json:"deviceInfo"`
	FailReason string    `gorm:"column:fail_reason;size:200" json:"failReason,omitempty"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
}

func (LoginLog) TableName() string {
	return "login_logs"
}

// UserSocialBinding 社交账号绑定
type UserSocialBinding struct {
	ID              string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	UserID          string    `gorm:"column:user_id;size:32;not null;uniqueIndex:idx_usb_user_provider" json:"userId"`
	Provider        string    `gorm:"column:provider;size:20;not null;uniqueIndex:idx_usb_user_provider;uniqueIndex:idx_usb_provider_uid" json:"provider"`
	ProviderUserID  string    `gorm:"column:provider_user_id;size:128;not null;uniqueIndex:idx_usb_provider_uid" json:"providerUserId"`
	AccessToken     string    `gorm:"column:access_token" json:"-"`
	RefreshTokenStr string    `gorm:"column:refresh_token" json:"-"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
}

func (UserSocialBinding) TableName() string {
	return "user_social_bindings"
}
