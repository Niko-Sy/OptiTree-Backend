package model

import "time"

type User struct {
	ID           string     `gorm:"primaryKey;column:id;size:32" json:"id"`
	Username     string     `gorm:"column:username;size:20;uniqueIndex;not null" json:"username"`
	DisplayName  string     `gorm:"column:display_name;size:30;not null" json:"displayName"`
	Email        string     `gorm:"column:email;size:100;uniqueIndex;not null" json:"email"`
	PasswordHash string     `gorm:"column:password_hash;not null" json:"-"`
	Avatar       *string    `gorm:"column:avatar" json:"avatar"`
	Role         string     `gorm:"column:role;size:10;default:'user'" json:"role"`
	Status       string     `gorm:"column:status;size:10;default:'active'" json:"status"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
	LastLoginAt  *time.Time `gorm:"column:last_login_at" json:"lastLoginAt"`
}

func (User) TableName() string {
	return "users"
}
