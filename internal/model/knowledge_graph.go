package model

import "gorm.io/datatypes"

// KnowledgeGraphNode 知识图谱节点，复合主键 (id, project_id)
type KnowledgeGraphNode struct {
	ID          string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID   string         `gorm:"primaryKey;column:project_id;size:32;not null" json:"projectId"`
	Type        string         `gorm:"column:type;size:20;not null" json:"type"`
	PositionX   float64        `gorm:"column:position_x;not null" json:"positionX"`
	PositionY   float64        `gorm:"column:position_y;not null" json:"positionY"`
	Label       string         `gorm:"column:label;size:60;not null" json:"label"`
	EntityType  string         `gorm:"column:entity_type;size:20;not null" json:"entityType"`
	Description *string        `gorm:"column:description;size:200" json:"description,omitempty"`
	SourceDoc   *string        `gorm:"column:source_doc;size:255" json:"sourceDoc,omitempty"`
	StyleJson   datatypes.JSON `gorm:"column:style_json;type:jsonb" json:"style,omitempty"`
	DataExtJson datatypes.JSON `gorm:"column:data_ext_json;type:jsonb" json:"dataExt,omitempty"`
}

func (KnowledgeGraphNode) TableName() string {
	return "knowledge_graph_nodes"
}

// KnowledgeGraphEdge 知识图谱边，复合主键 (id, project_id)
type KnowledgeGraphEdge struct {
	ID               string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	ProjectID        string         `gorm:"primaryKey;column:project_id;size:32;not null" json:"projectId"`
	SourceNodeID     string         `gorm:"column:source_node_id;size:32;not null" json:"source"`
	TargetNodeID     string         `gorm:"column:target_node_id;size:32;not null" json:"target"`
	Label            *string        `gorm:"column:label;size:30" json:"label,omitempty"`
	Type             string         `gorm:"column:type;size:20;default:'smoothstep'" json:"type"`
	Animated         bool           `gorm:"column:animated;default:false" json:"animated"`
	StyleJson        datatypes.JSON `gorm:"column:style_json;type:jsonb" json:"style,omitempty"`
	LabelStyleJson   datatypes.JSON `gorm:"column:label_style_json;type:jsonb" json:"labelStyle,omitempty"`
	LabelBgStyleJson datatypes.JSON `gorm:"column:label_bg_style_json;type:jsonb" json:"labelBgStyle,omitempty"`
}

func (KnowledgeGraphEdge) TableName() string {
	return "knowledge_graph_edges"
}
