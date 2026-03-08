package model

import (
	"time"

	"gorm.io/datatypes"
)

type VersionSnapshot struct {
	ID           string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID    string         `gorm:"column:project_id;size:32;not null;index" json:"projectId"`
	ProjectType  string         `gorm:"column:project_type;size:2;not null" json:"projectType"`
	Label        string         `gorm:"column:label;size:100;not null" json:"label"`
	CreatedAt    time.Time      `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	CreatedBy    string         `gorm:"column:created_by;size:32;not null" json:"createdBy"`
	SnapshotJson datatypes.JSON `gorm:"column:snapshot_json;type:jsonb;not null" json:"snapshot"`
}

func (VersionSnapshot) TableName() string {
	return "version_snapshots"
}
