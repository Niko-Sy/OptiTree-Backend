package graph_ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/repository"
)

var (
	ErrInvalidParameters   = errors.New("invalid tool parameters")
	ErrUnknownTool         = errors.New("unknown tool")
	ErrNodeNotFound        = errors.New("node not found")
	ErrPermissionDenied    = errors.New("permission denied")
	ErrUnsupportedGraph    = errors.New("unsupported graph type")
	ErrOperationNotAllowed = errors.New("operation not allowed")
)

func ValidateParameters(toolName string, args json.RawMessage) error {
	def, ok := GetTool(toolName)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTool, toolName)
	}

	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" {
		trimmed = "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("%w: args must be valid json", ErrInvalidParameters)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidParameters, err)
	}

	switch strings.ToLower(strings.TrimSpace(def.Name)) {
	case "update_node":
		if strFrom(payload, "nodeId") == "" {
			return fmt.Errorf("%w: nodeId is required", ErrInvalidParameters)
		}
		if v, ok := payload["name"]; ok {
			if len(SanitizeStringParam(anyToString(v), 60)) == 0 {
				return fmt.Errorf("%w: name cannot be empty when provided", ErrInvalidParameters)
			}
		}
	case "update_gate":
		if strFrom(payload, "nodeId") == "" {
			return fmt.Errorf("%w: nodeId is required", ErrInvalidParameters)
		}
		if !isValidGateType(strFrom(payload, "gateType")) {
			return fmt.Errorf("%w: gateType must be AND|OR|VOTE", ErrInvalidParameters)
		}
	case "add_node":
		if SanitizeStringParam(strFrom(payload, "name"), 60) == "" {
			return fmt.Errorf("%w: name is required", ErrInvalidParameters)
		}
		if !isValidAddNodeType(strFrom(payload, "nodeType")) {
			return fmt.Errorf("%w: nodeType invalid", ErrInvalidParameters)
		}
	case "add_gate":
		if !isValidGateType(strFrom(payload, "gateType")) {
			return fmt.Errorf("%w: gateType must be AND|OR|VOTE", ErrInvalidParameters)
		}
		parentID := strFrom(payload, "parentId")
		if parentID == "" {
			parentID = strFrom(payload, "parentNodeId")
		}
		if parentID == "" {
			return fmt.Errorf("%w: parentId is required", ErrInvalidParameters)
		}
		if len(addGateChildIDs(payload)) == 0 {
			return fmt.Errorf("%w: childIds is required", ErrInvalidParameters)
		}
	case "delete_node":
		if strFrom(payload, "nodeId") == "" {
			return fmt.Errorf("%w: nodeId is required", ErrInvalidParameters)
		}
	case "move_node":
		if strFrom(payload, "nodeId") == "" || strFrom(payload, "newParentId") == "" {
			return fmt.Errorf("%w: nodeId and newParentId are required", ErrInvalidParameters)
		}
	case "get_graph_snapshot":
		if v, ok := intFrom(payload, "maxNodes"); ok && v <= 0 {
			return fmt.Errorf("%w: maxNodes must be > 0", ErrInvalidParameters)
		}
		if v, ok := intFrom(payload, "maxEdges"); ok && v <= 0 {
			return fmt.Errorf("%w: maxEdges must be > 0", ErrInvalidParameters)
		}
	case "get_node_detail":
		if strFrom(payload, "nodeId") == "" {
			return fmt.Errorf("%w: nodeId is required", ErrInvalidParameters)
		}
	case "get_subtree":
		if strFrom(payload, "rootNodeId") == "" {
			return fmt.Errorf("%w: rootNodeId is required", ErrInvalidParameters)
		}
		if v, ok := intFrom(payload, "maxDepth"); ok {
			if v <= 0 || v > 8 {
				return fmt.Errorf("%w: maxDepth must be in [1,8]", ErrInvalidParameters)
			}
		}
	case "check_gate_semantics", "validate_fta_constraints":
		// optional args
	case "batch_operations":
		opList := objectSliceFrom(payload, "operations")
		if len(opList) == 0 {
			return fmt.Errorf("%w: operations is required", ErrInvalidParameters)
		}
		if len(opList) > 50 {
			return fmt.Errorf("%w: operations too many", ErrInvalidParameters)
		}
		for _, op := range opList {
			subTool := strFrom(op, "tool")
			if subTool == "" {
				return fmt.Errorf("%w: operations[].tool is required", ErrInvalidParameters)
			}
			if strings.EqualFold(subTool, "batch_operations") {
				return fmt.Errorf("%w: nested batch_operations is forbidden", ErrOperationNotAllowed)
			}
			rawArgs, err := json.Marshal(op["args"])
			if err != nil {
				return fmt.Errorf("%w: operations[].args invalid", ErrInvalidParameters)
			}
			if err := ValidateParameters(subTool, rawArgs); err != nil {
				return err
			}
		}
	case "highlight_nodes":
		if len(strSliceFrom(payload, "nodeIds")) == 0 {
			return fmt.Errorf("%w: nodeIds is required", ErrInvalidParameters)
		}
	case "locate_node", "expand_subtree", "collapse_subtree", "show_node_detail", "annotate_node":
		if strFrom(payload, "nodeId") == "" {
			return fmt.Errorf("%w: nodeId is required", ErrInvalidParameters)
		}
	case "suggest_batch_label_fix", "suggest_gate_corrections", "suggest_layout_optimization", "suggest_node_merge", "preview_layout":
		// optional args
	default:
		return fmt.Errorf("%w: %s", ErrUnknownTool, toolName)
	}

	return nil
}

func ValidateNodeExists(projectID, graphType, nodeID string, graphRepo *repository.GraphRepository) error {
	if graphRepo == nil {
		return fmt.Errorf("%w: graph repository is nil", ErrInvalidParameters)
	}
	projectID = strings.TrimSpace(projectID)
	nodeID = strings.TrimSpace(nodeID)
	if projectID == "" || nodeID == "" {
		return fmt.Errorf("%w: projectID and nodeID are required", ErrInvalidParameters)
	}

	switch strings.TrimSpace(graphType) {
	case "faultTree":
		nodes, _, err := graphRepo.GetFaultTreeGraph(projectID)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if strings.TrimSpace(n.ID) == nodeID {
				return nil
			}
		}
	case "knowledgeGraph":
		nodes, _, err := graphRepo.GetKnowledgeGraphGraph(projectID)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if strings.TrimSpace(n.ID) == nodeID {
				return nil
			}
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedGraph, graphType)
	}

	return fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
}

func ValidatePermissionBoundary(userID, projectID string, memberRepo *repository.MemberRepository) error {
	if memberRepo == nil {
		return fmt.Errorf("%w: member repository is nil", ErrInvalidParameters)
	}
	userID = strings.TrimSpace(userID)
	projectID = strings.TrimSpace(projectID)
	if userID == "" || projectID == "" {
		return fmt.Errorf("%w: userID and projectID are required", ErrInvalidParameters)
	}

	member, err := memberRepo.FindByProjectAndUser(projectID, userID)
	if err != nil {
		return err
	}
	if member == nil {
		return ErrPermissionDenied
	}
	if constant.RoleWeight[member.Role] < constant.RoleWeight[constant.RoleEditor] {
		return ErrPermissionDenied
	}
	return nil
}

func SanitizeStringParam(s string, maxLen int) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(s, "\u0000", ""))
	if maxLen > 0 {
		runes := []rune(trimmed)
		if len(runes) > maxLen {
			trimmed = string(runes[:maxLen])
		}
	}
	return trimmed
}

func strFrom(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	return strings.TrimSpace(anyToString(v))
}

func strSliceFrom(m map[string]interface{}, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s := strings.TrimSpace(anyToString(item))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func addGateChildIDs(payload map[string]interface{}) []string {
	if ids := strSliceFrom(payload, "childIds"); len(ids) > 0 {
		return ids
	}
	if ids := strSliceFrom(payload, "childNodeIds"); len(ids) > 0 {
		return ids
	}
	if ids := strSliceFrom(payload, "children"); len(ids) > 0 {
		return ids
	}
	return nil
}

func objectSliceFrom(m map[string]interface{}, key string) []map[string]interface{} {
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if obj, ok := item.(map[string]interface{}); ok {
			out = append(out, obj)
		}
	}
	return out
}

func anyToString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return strings.Trim(string(b), "\"")
	}
}

func intFrom(m map[string]interface{}, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	default:
		return 0, false
	}
}

func isValidGateType(raw string) bool {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "AND", "OR", "VOTE":
		return true
	default:
		return false
	}
}

func isValidNodeType(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "topEvent", "midEvent", "basicEvent", "gate":
		return true
	default:
		return false
	}
}

func isValidAddNodeType(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "topEvent", "midEvent", "basicEvent":
		return true
	default:
		return false
	}
}
