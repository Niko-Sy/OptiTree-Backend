package graph_ops

import (
	"encoding/json"
	"strings"

	"optitree-backend/internal/ai"
)

type ExecutionTier string

const (
	TierServer ExecutionTier = "server"
	TierClient ExecutionTier = "client"
	TierHybrid ExecutionTier = "hybrid"
)

type ToolDefinition struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Tier             ExecutionTier   `json:"tier"`
	RequireConfirm   bool            `json:"requireConfirm"`
	ConfirmThreshold int             `json:"confirmThreshold,omitempty"`
	Parameters       json.RawMessage `json:"parameters"`
	GraphTypes       []string        `json:"graphTypes,omitempty"`
}

var toolRegistry = []ToolDefinition{
	// Tier 1 - Server tools.
	{
		Name:           "update_node",
		Description:    "Update node basic attributes such as name, description, and investigate method",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId":            map[string]interface{}{"type": "string"},
				"name":              map[string]interface{}{"type": "string", "maxLength": 60},
				"description":       map[string]interface{}{"type": "string", "maxLength": 2000},
				"investigateMethod": map[string]interface{}{"type": "string", "maxLength": 2000},
			},
			"required": []string{"nodeId"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "update_gate",
		Description:    "Change gate type of a gate node",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId":   map[string]interface{}{"type": "string"},
				"gateType": map[string]interface{}{"type": "string", "enum": []string{"AND", "OR", "VOTE"}},
			},
			"required": []string{"nodeId", "gateType"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "add_node",
		Description:    "Add a new event node (top/mid/basic) and attach to a valid parent",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "maxLength": 60},
				"nodeType":    map[string]interface{}{"type": "string", "enum": []string{"topEvent", "midEvent", "basicEvent"}},
				"description": map[string]interface{}{"type": "string", "maxLength": 2000},
				"parentId":    map[string]interface{}{"type": "string"},
			},
			"required": []string{"name", "nodeType"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "add_gate",
		Description:    "Insert a gate node and rewire children under it",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"gateType": map[string]interface{}{"type": "string", "enum": []string{"AND", "OR", "VOTE"}},
				"parentId": map[string]interface{}{"type": "string"},
				"childIds": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"type": "string"},
				},
			},
			"required": []string{"gateType", "parentId", "childIds"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "delete_node",
		Description:    "Delete a node and optionally delete all descendants",
		Tier:           TierServer,
		RequireConfirm: true,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId":         map[string]interface{}{"type": "string"},
				"deleteChildren": map[string]interface{}{"type": "boolean"},
			},
			"required": []string{"nodeId"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "move_node",
		Description:    "Change the parent node of a node",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId":      map[string]interface{}{"type": "string"},
				"newParentId": map[string]interface{}{"type": "string"},
			},
			"required": []string{"nodeId", "newParentId"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:             "batch_operations",
		Description:      "Execute multiple server operations atomically",
		Tier:             TierServer,
		RequireConfirm:   true,
		ConfirmThreshold: 5,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"operations": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"tool": map[string]interface{}{"type": "string"},
							"args": map[string]interface{}{"type": "object"},
						},
						"required": []string{"tool", "args"},
					},
				},
			},
			"required": []string{"operations"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "get_graph_snapshot",
		Description:    "Read full fault-tree snapshot (nodes and edges) for reasoning and verification",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"maxNodes": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 2000},
				"maxEdges": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 3000},
			},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "get_node_detail",
		Description:    "Read one node detail with incoming/outgoing relations",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId": map[string]interface{}{"type": "string"},
			},
			"required": []string{"nodeId"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "get_subtree",
		Description:    "Read subtree rooted at a node for focused analysis",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"rootNodeId": map[string]interface{}{"type": "string"},
				"maxDepth":   map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 8},
			},
			"required": []string{"rootNodeId"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "check_gate_semantics",
		Description:    "Check gate semantics (AND/OR/VOTE validity and child cardinality)",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId": map[string]interface{}{"type": "string"},
			},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "validate_fta_constraints",
		Description:    "Validate IEC61025-oriented fault-tree constraints and return issues",
		Tier:           TierServer,
		RequireConfirm: false,
		Parameters: mustJSON(map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}),
		GraphTypes: []string{"faultTree"},
	},

	// Tier 2 - Client tools.
	{
		Name: "highlight_nodes", Description: "Highlight nodes on canvas", Tier: TierClient,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeIds": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"color":   map[string]interface{}{"type": "string"},
			},
			"required": []string{"nodeIds"},
		}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "locate_node", Description: "Focus viewport to a node", Tier: TierClient,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"},
		}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "expand_subtree", Description: "Expand a node subtree", Tier: TierClient,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"}}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "collapse_subtree", Description: "Collapse a node subtree", Tier: TierClient,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"}}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "show_node_detail", Description: "Open node detail panel", Tier: TierClient,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"}}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "preview_layout", Description: "Preview an alternate layout without persisting", Tier: TierClient,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"layout": map[string]interface{}{"type": "object"}}}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "annotate_node", Description: "Attach temporary annotation on node", Tier: TierClient,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId": map[string]interface{}{"type": "string"},
				"text":   map[string]interface{}{"type": "string", "maxLength": 200},
			},
			"required": []string{"nodeId", "text"},
		}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},

	// Tier 3 - Hybrid tools.
	{
		Name: "suggest_batch_label_fix", Description: "Suggest batch label cleanup operations", Tier: TierHybrid,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name: "suggest_gate_corrections", Description: "Suggest gate corrections based on topology hints", Tier: TierHybrid,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name: "suggest_layout_optimization", Description: "Suggest layout optimization preview", Tier: TierHybrid,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name: "suggest_node_merge", Description: "Suggest duplicate node merge candidates", Tier: TierHybrid,
		Parameters: mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes: []string{"faultTree"},
	},
}

func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func GetTool(name string) (*ToolDefinition, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, def := range toolRegistry {
		if strings.EqualFold(def.Name, normalized) {
			cpy := def
			return &cpy, true
		}
	}
	return nil, false
}

func GetToolsForGraphType(graphType string) []ToolDefinition {
	normalized := strings.TrimSpace(graphType)
	out := make([]ToolDefinition, 0, len(toolRegistry))
	for _, def := range toolRegistry {
		if supportsGraphType(def.GraphTypes, normalized) {
			out = append(out, def)
		}
	}
	return out
}

func ToOAITools(defs []ToolDefinition) []ai.OAIToolDef {
	out := make([]ai.OAIToolDef, 0, len(defs))
	for _, def := range defs {
		out = append(out, ai.OAIToolDef{
			Type: "function",
			Function: ai.OAIToolFuncDef{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
		})
	}
	return out
}

func supportsGraphType(allowed []string, graphType string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(graphType)) {
			return true
		}
	}
	return false
}

func ToolSupportsGraphType(def *ToolDefinition, graphType string) bool {
	if def == nil {
		return false
	}
	return supportsGraphType(def.GraphTypes, graphType)
}

func ToolMutatesGraph(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "update_node", "update_gate", "add_node", "add_gate", "delete_node", "move_node", "batch_operations":
		return true
	default:
		return false
	}
}
