package graph_ops

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"optitree-backend/internal/ai"
)

type ExecutionTier string
type ToolKind string

const (
	TierServer ExecutionTier = "server"
	TierClient ExecutionTier = "client"
	TierHybrid ExecutionTier = "hybrid"

	ToolKindReadContext   ToolKind = "read_context"
	ToolKindValidate      ToolKind = "validate"
	ToolKindMutate        ToolKind = "mutate"
	ToolKindClientUI      ToolKind = "client_ui"
	ToolKindHybridPreview ToolKind = "hybrid_preview"
)

type ToolDefinition struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Tier             ExecutionTier   `json:"tier"`
	Kind             ToolKind        `json:"kind,omitempty"`
	ReadOnly         bool            `json:"readOnly"`
	MutatesGraph     bool            `json:"mutatesGraph"`
	RequiresRead     bool            `json:"requiresRead"`
	Experimental     bool            `json:"experimental"`
	ProductionReady  bool            `json:"productionReady"`
	RequireConfirm   bool            `json:"requireConfirm"`
	ConfirmThreshold int             `json:"confirmThreshold,omitempty"`
	PromptExample    string          `json:"promptExample,omitempty"`
	Parameters       json.RawMessage `json:"parameters"`
	GraphTypes       []string        `json:"graphTypes,omitempty"`
}

var toolRegistry = []ToolDefinition{
	// Tier 1 - Server tools.
	{
		Name:           "update_node",
		Description:    "Update node basic attributes such as name, description, and investigate method",
		Tier:           TierServer,
		MutatesGraph:   true,
		RequiresRead:   true,
		RequireConfirm: false,
		PromptExample:  `{"nodeId":"n1","name":"修正后的节点名称"}`,
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
		MutatesGraph:   true,
		RequiresRead:   true,
		RequireConfirm: false,
		PromptExample:  `{"nodeId":"g1","gateType":"OR"}`,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nodeId":   map[string]interface{}{"type": "string"},
				"gateType": map[string]interface{}{"type": "string", "enum": []string{"AND", "OR", "NOT"}},
			},
			"required": []string{"nodeId", "gateType"},
		}),
		GraphTypes: []string{"faultTree"},
	},
	{
		Name:           "add_node",
		Description:    "Add a new event node (top/mid/basic) and attach to a valid parent",
		Tier:           TierServer,
		MutatesGraph:   true,
		RequiresRead:   true,
		RequireConfirm: false,
		PromptExample:  `{"name":"泵失效","nodeType":"basicEvent","parentId":"g1"}`,
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
		MutatesGraph:   true,
		RequiresRead:   true,
		RequireConfirm: false,
		PromptExample:  `{"gateType":"AND","parentId":"p1","childIds":["c1","c2"]}`,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"gateType": map[string]interface{}{"type": "string", "enum": []string{"AND", "OR", "NOT"}},
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
		MutatesGraph:   true,
		RequiresRead:   true,
		RequireConfirm: true,
		PromptExample:  `{"nodeId":"n1","deleteChildren":false}`,
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
		MutatesGraph:   true,
		RequiresRead:   true,
		RequireConfirm: false,
		PromptExample:  `{"nodeId":"n1","newParentId":"g2"}`,
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
		Description:      "Execute multiple server operations atomically; supports repairMode for phased restructuring",
		Tier:             TierServer,
		MutatesGraph:     true,
		RequiresRead:     true,
		RequireConfirm:   true,
		ConfirmThreshold: 5,
		PromptExample:    `{"operations":[{"tool":"update_node","args":{"nodeId":"n1","name":"新名称"}}],"repairMode":false}`,
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
				"repairMode": map[string]interface{}{
					"type":        "boolean",
					"description": "Allow intermediate invalid states during phased repairs; only fatal graph errors remain blocking.",
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
		ReadOnly:       true,
		RequireConfirm: false,
		PromptExample:  `{}`,
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
		ReadOnly:       true,
		RequireConfirm: false,
		PromptExample:  `{"nodeId":"n1"}`,
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
		ReadOnly:       true,
		RequireConfirm: false,
		PromptExample:  `{"rootNodeId":"n1","maxDepth":3}`,
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
		Description:    "Check gate semantics (AND/OR/NOT validity and child cardinality)",
		Tier:           TierServer,
		ReadOnly:       true,
		RequireConfirm: false,
		PromptExample:  `{"nodeId":"g1"}`,
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
		ReadOnly:       true,
		RequireConfirm: false,
		PromptExample:  `{}`,
		Parameters: mustJSON(map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}),
		GraphTypes: []string{"faultTree"},
	},

	// Tier 2 - Client tools.
	{
		Name: "highlight_nodes", Description: "Highlight nodes on canvas", Tier: TierClient,
		ReadOnly:      true,
		PromptExample: `{"nodeIds":["n1","n2"],"color":"#ff9800"}`,
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
		ReadOnly:      true,
		PromptExample: `{"nodeId":"n1"}`,
		Parameters: mustJSON(map[string]interface{}{
			"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"},
		}),
		GraphTypes: []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "expand_subtree", Description: "Expand a node subtree", Tier: TierClient,
		ReadOnly:      true,
		PromptExample: `{"nodeId":"n1"}`,
		Parameters:    mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"}}),
		GraphTypes:    []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "collapse_subtree", Description: "Collapse a node subtree", Tier: TierClient,
		ReadOnly:      true,
		PromptExample: `{"nodeId":"n1"}`,
		Parameters:    mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"}}),
		GraphTypes:    []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "show_node_detail", Description: "Open node detail panel", Tier: TierClient,
		ReadOnly:      true,
		PromptExample: `{"nodeId":"n1"}`,
		Parameters:    mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"nodeId": map[string]interface{}{"type": "string"}}, "required": []string{"nodeId"}}),
		GraphTypes:    []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "preview_layout", Description: "Preview an alternate layout without persisting", Tier: TierClient,
		ReadOnly:      true,
		PromptExample: `{"layout":{"mode":"hierarchical"}}`,
		Parameters:    mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{"layout": map[string]interface{}{"type": "object"}}}),
		GraphTypes:    []string{"faultTree", "knowledgeGraph"},
	},
	{
		Name: "annotate_node", Description: "Attach temporary annotation on node", Tier: TierClient,
		ReadOnly:      true,
		PromptExample: `{"nodeId":"n1","text":"建议补充边界条件"}`,
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
		Experimental:    true,
		ProductionReady: true,
		RequiresRead:    true,
		PromptExample:   `{}`,
		Parameters:      mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes:      []string{"faultTree"},
	},
	{
		Name: "suggest_gate_corrections", Description: "Suggest gate corrections based on topology hints", Tier: TierHybrid,
		Experimental:  true,
		RequiresRead:  true,
		PromptExample: `{}`,
		Parameters:    mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes:    []string{"faultTree"},
	},
	{
		Name: "suggest_layout_optimization", Description: "Suggest layout optimization preview", Tier: TierHybrid,
		Experimental:  true,
		RequiresRead:  true,
		PromptExample: `{}`,
		Parameters:    mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes:    []string{"faultTree"},
	},
	{
		Name: "suggest_node_merge", Description: "Suggest duplicate node merge candidates", Tier: TierHybrid,
		Experimental:  true,
		RequiresRead:  true,
		PromptExample: `{}`,
		Parameters:    mustJSON(map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}),
		GraphTypes:    []string{"faultTree"},
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
			cpy := normalizeToolDefinition(def)
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
			out = append(out, normalizeToolDefinition(def))
		}
	}
	return out
}

func FilterToolsForMode(graphType string, readOnly bool, includeHybridTools bool) []ToolDefinition {
	defs := GetToolsForGraphType(graphType)
	out := make([]ToolDefinition, 0, len(defs))
	for _, def := range defs {
		if def.Tier == TierHybrid && !includeHybridTools {
			continue
		}

		if readOnly {
			if def.MutatesGraph {
				continue
			}
			if def.Tier == TierHybrid {
				continue
			}
			if strings.EqualFold(def.Name, "annotate_node") || strings.EqualFold(def.Name, "preview_layout") {
				continue
			}
		}

		out = append(out, def)
	}
	return out
}

func BuildToolPromptGuide(defs []ToolDefinition) string {
	if len(defs) == 0 {
		return "No tool is available for this turn."
	}

	lines := make([]string, 0, len(defs)+8)
	lines = append(lines,
		"Use only tools listed below; names and parameters are authoritative.",
		"Contract order for graph edits: read_context -> validate -> mutate -> validate.",
	)

	order := []ToolKind{ToolKindReadContext, ToolKindValidate, ToolKindMutate, ToolKindClientUI, ToolKindHybridPreview}
	grouped := make(map[ToolKind][]ToolDefinition, len(order))
	for _, def := range defs {
		def = normalizeToolDefinition(def)
		grouped[def.Kind] = append(grouped[def.Kind], def)
	}

	for _, kind := range order {
		items := grouped[kind]
		if len(items) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s]", kind))
		for _, def := range items {
			required := requiredParamNames(def.Parameters)
			requiredHint := "none"
			if len(required) > 0 {
				requiredHint = strings.Join(required, ",")
			}
			flags := make([]string, 0, 4)
			if def.ReadOnly {
				flags = append(flags, "read_only")
			}
			if def.MutatesGraph {
				flags = append(flags, "mutates_graph")
			}
			if def.RequiresRead {
				flags = append(flags, "requires_read")
			}
			if def.RequireConfirm {
				flags = append(flags, "requires_confirm")
			}
			if def.Experimental {
				flags = append(flags, "experimental")
			}
			if def.ProductionReady {
				flags = append(flags, "production_ready")
			}
			if len(flags) == 0 {
				flags = append(flags, "none")
			}

			line := fmt.Sprintf("- %s | required=[%s] | flags=[%s] | %s", def.Name, requiredHint, strings.Join(flags, ","), strings.TrimSpace(def.Description))
			lines = append(lines, line)
		}
	}

	return strings.Join(lines, "\n")
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
	def, ok := GetTool(toolName)
	if !ok {
		return false
	}
	return def.MutatesGraph
}

func ToolIsReadContext(toolName string) bool {
	def, ok := GetTool(toolName)
	if !ok {
		return false
	}
	return def.Kind == ToolKindReadContext
}

func normalizeToolDefinition(def ToolDefinition) ToolDefinition {
	if def.Kind != "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(def.Name)) {
	case "get_graph_snapshot", "get_node_detail", "get_subtree":
		def.Kind = ToolKindReadContext
	case "check_gate_semantics", "validate_fta_constraints":
		def.Kind = ToolKindValidate
	default:
		switch def.Tier {
		case TierHybrid:
			def.Kind = ToolKindHybridPreview
		case TierClient:
			def.Kind = ToolKindClientUI
		default:
			if def.MutatesGraph {
				def.Kind = ToolKindMutate
			}
		}
	}
	return def
}

func requiredParamNames(schemaRaw json.RawMessage) []string {
	if len(schemaRaw) == 0 {
		return nil
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return nil
	}
	required, ok := schema["required"].([]interface{})
	if !ok || len(required) == 0 {
		return nil
	}

	out := make([]string, 0, len(required))
	for _, item := range required {
		s := strings.TrimSpace(fmt.Sprintf("%v", item))
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
