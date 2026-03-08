package model

import (
	"time"

	"gorm.io/datatypes"
)

type Notification struct {
	ID         string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	UserID     string         `gorm:"column:user_id;size:32;not null;index" json:"userId"`
	Type       string         `gorm:"column:type;size:50;not null" json:"type"`
	Title      string         `gorm:"column:title;size:200;not null" json:"title"`
	Content    string         `gorm:"column:content;not null" json:"content"`
	IsRead     bool           `gorm:"column:is_read;default:false" json:"isRead"`
	ProjectID  *string        `gorm:"column:project_id;size:32" json:"projectId,omitempty"`
	ResourceID *string        `gorm:"column:resource_id;size:32" json:"resourceId,omitempty"`
	ExtraJson  datatypes.JSON `gorm:"column:extra_json;type:jsonb" json:"extra,omitempty"`
	CreatedAt  time.Time      `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
}

func (Notification) TableName() string {
	return "notifications"
}

type AuditLog struct {
	ID           string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	UserID       *string   `gorm:"column:user_id;size:32;index" json:"userId,omitempty"`
	OperatorName string    `gorm:"column:operator_name;size:50;not null" json:"operatorName"`
	Action       string    `gorm:"column:action;size:60;not null" json:"action"`
	ResourceType string    `gorm:"column:resource_type;size:40;not null" json:"resourceType"`
	ResourceID   string    `gorm:"column:resource_id;size:32;not null" json:"resourceId"`
	Summary      string    `gorm:"column:summary" json:"summary"`
	IPAddress    string    `gorm:"column:ip_address;size:45" json:"ipAddress"`
	UserAgent    string    `gorm:"column:user_agent;size:500" json:"userAgent"`
	ProjectID    *string   `gorm:"column:project_id;size:32;index" json:"projectId,omitempty"`
	CreatedAt    time.Time `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
}

func (AuditLog) TableName() string {
	return "audit_logs"
}
