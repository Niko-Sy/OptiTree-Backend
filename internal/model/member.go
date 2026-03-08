package model

import "time"

type ProjectMember struct {
	ID        string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID string    `gorm:"column:project_id;size:32;not null;index:idx_pm_proj_user" json:"projectId"`
	UserID    string    `gorm:"column:user_id;size:32;not null;index:idx_pm_proj_user" json:"userId"`
	Role      string    `gorm:"column:role;size:10;not null" json:"role"`
	Status    string    `gorm:"column:status;size:10;default:'active'" json:"status"`
	JoinedAt  time.Time `gorm:"column:joined_at;not null;autoCreateTime" json:"joinedAt"`

	// 关联用户信息（用于列表展示，不存库）
	User *User `gorm:"foreignKey:UserID;references:ID" json:"user,omitempty"`
}

func (ProjectMember) TableName() string {
	return "project_members"
}

type Invitation struct {
	ID        string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID string    `gorm:"column:project_id;size:32;not null" json:"projectId"`
	Email     string    `gorm:"column:email;size:100;not null" json:"email"`
	Role      string    `gorm:"column:role;size:10;not null" json:"role"`
	Status    string    `gorm:"column:status;size:20;not null;default:'pending'" json:"status"`
	Token     string    `gorm:"column:token;size:128;uniqueIndex;not null" json:"token"`
	InvitedBy string    `gorm:"column:invited_by;size:32;not null" json:"invitedBy"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null" json:"expiresAt"`
	CreatedAt time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
}

func (Invitation) TableName() string {
	return "invitations"
}
