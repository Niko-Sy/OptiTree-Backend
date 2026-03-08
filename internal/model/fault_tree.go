package model

import (
	"github.com/lib/pq"
	"gorm.io/datatypes"
)

// FaultTreeNode 故障树节点，复合主键 (id, project_id)
type FaultTreeNode struct {
	ID                string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID         string         `gorm:"primaryKey;column:project_id;size:32;not null" json:"projectId"`
	Type              string         `gorm:"column:type;size:20;not null" json:"type"`
	Name              string         `gorm:"column:name;size:60;not null" json:"name"`
	X                 float64        `gorm:"column:x;not null" json:"x"`
	Y                 float64        `gorm:"column:y;not null" json:"y"`
	Width             float64        `gorm:"column:width;not null" json:"width"`
	Height            float64        `gorm:"column:height;not null" json:"height"`
	Probability       *float64       `gorm:"column:probability" json:"probability,omitempty"`
	GateType          *string        `gorm:"column:gate_type;size:10" json:"gateType,omitempty"`
	EventID           *string        `gorm:"column:event_id;size:20" json:"eventId,omitempty"`
	Description       *string        `gorm:"column:description" json:"description,omitempty"`
	ErrorLevel        *string        `gorm:"column:error_level;size:10" json:"errorLevel,omitempty"`
	Priority          int            `gorm:"column:priority;not null;default:0" json:"priority"`
	ShowProbability   bool           `gorm:"column:show_probability;default:false" json:"showProbability"`
	Rules             datatypes.JSON `gorm:"column:rules;type:jsonb" json:"rules,omitempty"`
	InvestigateMethod *string        `gorm:"column:investigate_method" json:"investigateMethod,omitempty"`
	Documents         pq.StringArray `gorm:"column:documents;type:text[]" json:"documents,omitempty"`
	Transfer          *string        `gorm:"column:transfer;size:32" json:"transfer,omitempty"`
}

func (FaultTreeNode) TableName() string {
	return "fault_tree_nodes"
}

// FaultTreeEdge 故障树边，复合主键 (id, project_id)
type FaultTreeEdge struct {
	ID         string `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID  string `gorm:"primaryKey;column:project_id;size:32;not null" json:"projectId"`
	FromNodeID string `gorm:"column:from_node_id;size:32;not null" json:"from"`
	ToNodeID   string `gorm:"column:to_node_id;size:32;not null" json:"to"`
}

func (FaultTreeEdge) TableName() string {
	return "fault_tree_edges"
}
