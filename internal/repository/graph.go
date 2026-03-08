package repository

import (
	"optitree-backend/internal/model"

	"github.com/lib/pq"
	"gorm.io/gorm"
)

type GraphRepository struct {
	db *gorm.DB
}

func NewGraphRepository(db *gorm.DB) *GraphRepository {
	return &GraphRepository{db: db}
}

// GetFaultTreeGraph 获取故障树图数据
func (r *GraphRepository) GetFaultTreeGraph(projectID string) ([]model.FaultTreeNode, []model.FaultTreeEdge, error) {
	var nodes []model.FaultTreeNode
	var edges []model.FaultTreeEdge
	if err := r.db.Where("project_id = ?", projectID).Find(&nodes).Error; err != nil {
		return nil, nil, err
	}
	if err := r.db.Where("project_id = ?", projectID).Find(&edges).Error; err != nil {
		return nil, nil, err
	}
	return nodes, edges, nil
}

// BatchReplaceFaultTree 在事务中全量替换故障树数据
func (r *GraphRepository) BatchReplaceFaultTree(tx *gorm.DB, projectID string, nodes []model.FaultTreeNode, edges []model.FaultTreeEdge) error {
	if tx == nil {
		tx = r.db
	}
	for i := range nodes {
		if len(nodes[i].Rules) == 0 {
			nodes[i].Rules = []byte("[]")
		}
		if nodes[i].Documents == nil {
			nodes[i].Documents = pq.StringArray{}
		}
	}
	// 清空旧数据
	if err := tx.Where("project_id = ?", projectID).Delete(&model.FaultTreeNode{}).Error; err != nil {
		return err
	}
	if err := tx.Where("project_id = ?", projectID).Delete(&model.FaultTreeEdge{}).Error; err != nil {
		return err
	}
	// 批量插入新数据
	if len(nodes) > 0 {
		if err := tx.CreateInBatches(nodes, 200).Error; err != nil {
			return err
		}
	}
	if len(edges) > 0 {
		if err := tx.CreateInBatches(edges, 200).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetKnowledgeGraphGraph 获取知识图谱图数据
func (r *GraphRepository) GetKnowledgeGraphGraph(projectID string) ([]model.KnowledgeGraphNode, []model.KnowledgeGraphEdge, error) {
	var nodes []model.KnowledgeGraphNode
	var edges []model.KnowledgeGraphEdge
	if err := r.db.Where("project_id = ?", projectID).Find(&nodes).Error; err != nil {
		return nil, nil, err
	}
	if err := r.db.Where("project_id = ?", projectID).Find(&edges).Error; err != nil {
		return nil, nil, err
	}
	return nodes, edges, nil
}

// BatchReplaceKnowledgeGraph 在事务中全量替换知识图谱数据
func (r *GraphRepository) BatchReplaceKnowledgeGraph(tx *gorm.DB, projectID string, nodes []model.KnowledgeGraphNode, edges []model.KnowledgeGraphEdge) error {
	if tx == nil {
		tx = r.db
	}
	for i := range nodes {
		if len(nodes[i].StyleJson) == 0 {
			nodes[i].StyleJson = []byte("{}")
		}
		if len(nodes[i].DataExtJson) == 0 {
			nodes[i].DataExtJson = []byte("{}")
		}
	}
	for i := range edges {
		if len(edges[i].StyleJson) == 0 {
			edges[i].StyleJson = []byte("{}")
		}
		if len(edges[i].LabelStyleJson) == 0 {
			edges[i].LabelStyleJson = []byte("{}")
		}
		if len(edges[i].LabelBgStyleJson) == 0 {
			edges[i].LabelBgStyleJson = []byte("{}")
		}
	}
	if err := tx.Where("project_id = ?", projectID).Delete(&model.KnowledgeGraphNode{}).Error; err != nil {
		return err
	}
	if err := tx.Where("project_id = ?", projectID).Delete(&model.KnowledgeGraphEdge{}).Error; err != nil {
		return err
	}
	if len(nodes) > 0 {
		if err := tx.CreateInBatches(nodes, 200).Error; err != nil {
			return err
		}
	}
	if len(edges) > 0 {
		if err := tx.CreateInBatches(edges, 200).Error; err != nil {
			return err
		}
	}
	return nil
}

// DeleteFaultTreeByProject 删除项目的全部故障树数据
func (r *GraphRepository) DeleteFaultTreeByProject(tx *gorm.DB, projectID string) error {
	if tx == nil {
		tx = r.db
	}
	if err := tx.Where("project_id = ?", projectID).Delete(&model.FaultTreeNode{}).Error; err != nil {
		return err
	}
	return tx.Where("project_id = ?", projectID).Delete(&model.FaultTreeEdge{}).Error
}

// DeleteKnowledgeGraphByProject 删除项目的全部知识图谱数据
func (r *GraphRepository) DeleteKnowledgeGraphByProject(tx *gorm.DB, projectID string) error {
	if tx == nil {
		tx = r.db
	}
	if err := tx.Where("project_id = ?", projectID).Delete(&model.KnowledgeGraphNode{}).Error; err != nil {
		return err
	}
	return tx.Where("project_id = ?", projectID).Delete(&model.KnowledgeGraphEdge{}).Error
}
