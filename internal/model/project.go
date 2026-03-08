package model

import (
	"time"

	"github.com/lib/pq"
)

type Project struct {
	ID              string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	Name            string         `gorm:"column:name;size:50;not null" json:"name"`
	Type            string         `gorm:"column:type;size:2;not null" json:"type"`
	Description     *string        `gorm:"column:description;size:200" json:"description"`
	Tags            pq.StringArray `gorm:"column:tags;type:text[]" json:"tags"`
	NodeCount       int            `gorm:"column:node_count;default:0" json:"nodeCount"`
	EdgeCount       int            `gorm:"column:edge_count;default:0" json:"edgeCount"`
	EntityCount     int            `gorm:"column:entity_count;default:0" json:"entityCount"`
	RelationCount   int            `gorm:"column:relation_count;default:0" json:"relationCount"`
	GraphRevision   int            `gorm:"column:graph_revision;default:0" json:"graphRevision"`
	LatestVersionID *string        `gorm:"column:latest_version_id;size:32" json:"latestVersionId"`
	MemberCount     int            `gorm:"column:member_count;default:0" json:"memberCount"`
	CreatedAt       time.Time      `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt       time.Time      `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
	CreatedBy       string         `gorm:"column:created_by;size:32;not null" json:"createdBy"`
}

func (Project) TableName() string {
	return "projects"
}
