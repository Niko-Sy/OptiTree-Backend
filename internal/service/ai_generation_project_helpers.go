package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"

	"github.com/lib/pq"
)

var (
	ErrProjectTypeMismatch      = errors.New("项目类型不匹配")
	ErrProjectPermissionDenied  = errors.New("无项目编辑权限")
	ErrAIGenerationResultFormat = errors.New("AI 生成结果格式不正确")
)

func (s *AITaskService) resolveOrCreateProject(
	ctx context.Context,
	inputProjectID *string,
	userID, projectType, nameHint string,
) (*model.Project, error) {
	if inputProjectID != nil && strings.TrimSpace(*inputProjectID) != "" {
		projectID := strings.TrimSpace(*inputProjectID)
		project, err := s.projectRepo.FindByID(projectID)
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, ErrProjectNotFound
		}
		if project.Type != projectType {
			return nil, ErrProjectTypeMismatch
		}
		member, err := s.memberRepo.FindByProjectAndUser(projectID, userID)
		if err != nil {
			return nil, err
		}
		if member == nil || constant.RoleWeight[member.Role] < constant.RoleWeight[constant.RoleEditor] {
			return nil, ErrProjectPermissionDenied
		}
		return project, nil
	}

	projectName := buildAIGeneratedProjectName(projectType, nameHint)
	return s.projectService.Create(ctx, userID, CreateProjectInput{
		Name:        projectName,
		Type:        projectType,
		Description: "AI 自动生成项目",
		Tags:        []string{"AI生成"},
	})
}

func buildAIGeneratedProjectName(projectType, hint string) string {
	timePart := time.Now().Format("20060102-150405")
	if trimmed := strings.TrimSpace(hint); trimmed != "" {
		runes := []rune(trimmed)
		if len(runes) > 24 {
			trimmed = string(runes[:24])
		}
		return fmt.Sprintf("AI-%s-%s", trimmed, timePart)
	}
	if projectType == constant.ProjectTypeFT {
		return fmt.Sprintf("AI-故障树-%s", timePart)
	}
	return fmt.Sprintf("AI-知识图谱-%s", timePart)
}

func (s *AITaskService) setProjectGenerationStatus(projectID, status string) error {
	st := status
	if err := s.projectRepo.UpdateGenerationStatus(projectID, &st); err != nil {
		return err
	}
	return nil
}

func (s *AITaskService) publishTaskEvent(evt TaskProgressEvent) {
	if s.progressHub == nil || evt.ProjectID == "" {
		return
	}
	if evt.UpdatedAt == "" {
		evt.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	s.progressHub.Publish(evt.ProjectID, evt)
}

func (s *AITaskService) saveGeneratedFaultTreeToProject(ctx context.Context, projectID string, result *ai.FaultTreeResult) error {
	nodes, edges, err := toFaultTreeGraph(result)
	if err != nil {
		return err
	}

	for i := 0; i < 2; i++ {
		project, err := s.projectRepo.FindByID(projectID)
		if err != nil {
			return err
		}
		if project == nil {
			return ErrProjectNotFound
		}
		_, err = s.ftService.SaveGraph(ctx, projectID, SaveFaultTreeInput{
			Nodes:    nodes,
			Edges:    edges,
			Revision: project.GraphRevision,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrVersionConflict) {
			return err
		}
	}
	return ErrVersionConflict
}

func (s *AITaskService) saveGeneratedKnowledgeGraphToProject(ctx context.Context, projectID string, result *ai.KnowledgeGraphResult) error {
	nodes, edges, err := toKnowledgeGraph(result)
	if err != nil {
		return err
	}

	for i := 0; i < 2; i++ {
		project, err := s.projectRepo.FindByID(projectID)
		if err != nil {
			return err
		}
		if project == nil {
			return ErrProjectNotFound
		}
		_, err = s.kgService.SaveGraph(ctx, projectID, SaveKnowledgeGraphInput{
			Nodes:    nodes,
			Edges:    edges,
			Revision: project.GraphRevision,
		})
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrVersionConflict) {
			return err
		}
	}
	return ErrVersionConflict
}

func toFaultTreeGraph(result *ai.FaultTreeResult) ([]model.FaultTreeNode, []model.FaultTreeEdge, error) {
	if result == nil || len(result.Nodes) == 0 {
		return nil, nil, ErrAIGenerationResultFormat
	}

	nodes := make([]model.FaultTreeNode, 0, len(result.Nodes))
	nodeIDMap := make(map[string]string, len(result.Nodes))

	for idx, raw := range result.Nodes {
		data := toMap(raw["data"])
		rawID := toString(raw["id"])
		if rawID == "" {
			rawID = fmt.Sprintf("n_%d", idx+1)
		}
		nodeID := normalizeBoundedID(rawID, "n", 32)
		nodeIDMap[rawID] = nodeID

		nodeType, gateType := mapFaultTreeNodeType(toString(raw["type"]), data)
		name := toString(raw["name"])
		if name == "" {
			name = toString(data["label"])
		}
		if name == "" {
			name = nodeID
		}

		x, y := pickXY(raw)
		width := toFloat(raw["width"], 140)
		height := toFloat(raw["height"], 60)
		if width <= 0 {
			width = 140
		}
		if height <= 0 {
			height = 60
		}

		prob := pickFloatPtr(raw["probability"])
		if prob == nil {
			prob = pickFloatPtr(data["probability"])
		}

		n := model.FaultTreeNode{
			ID:              nodeID,
			Type:            nodeType,
			Name:            name,
			X:               x,
			Y:               y,
			Width:           width,
			Height:          height,
			Probability:     prob,
			GateType:        gateType,
			Priority:        int(toFloat(raw["priority"], 0)),
			ShowProbability: toBool(raw["showProbability"]),
			Documents:       toStringArray(raw["documents"]),
		}

		if len(n.Documents) == 0 {
			n.Documents = pq.StringArray{}
		}

		if b, ok := marshalMaybe(raw["rules"], []byte("[]")); ok {
			n.Rules = b
		}
		if v := toString(raw["eventId"]); v != "" {
			n.EventID = ptr(v)
		}
		if v := toString(raw["description"]); v != "" {
			n.Description = ptr(v)
		}
		if v := toString(raw["errorLevel"]); v != "" {
			n.ErrorLevel = ptr(v)
		}
		if v := toString(raw["investigateMethod"]); v != "" {
			n.InvestigateMethod = ptr(v)
		}
		if v := toString(raw["transfer"]); v != "" {
			n.Transfer = ptr(v)
		}

		nodes = append(nodes, n)
	}

	edges := make([]model.FaultTreeEdge, 0, len(result.Edges))
	for idx, raw := range result.Edges {
		fromRaw := firstNonEmpty(toString(raw["from"]), toString(raw["source"]))
		toRaw := firstNonEmpty(toString(raw["to"]), toString(raw["target"]))
		if fromRaw == "" || toRaw == "" {
			continue
		}
		fromID := firstNonEmpty(nodeIDMap[fromRaw], normalizeBoundedID(fromRaw, "n", 32))
		toID := firstNonEmpty(nodeIDMap[toRaw], normalizeBoundedID(toRaw, "n", 32))
		rawID := toString(raw["id"])
		if rawID == "" {
			rawID = fmt.Sprintf("e_%d_%s_%s", idx+1, fromRaw, toRaw)
		}
		edges = append(edges, model.FaultTreeEdge{
			ID:         normalizeBoundedID(rawID, "e", 32),
			FromNodeID: fromID,
			ToNodeID:   toID,
		})
	}

	return nodes, edges, nil
}

func toKnowledgeGraph(result *ai.KnowledgeGraphResult) ([]model.KnowledgeGraphNode, []model.KnowledgeGraphEdge, error) {
	if result == nil || len(result.Nodes) == 0 {
		return nil, nil, ErrAIGenerationResultFormat
	}

	nodes := make([]model.KnowledgeGraphNode, 0, len(result.Nodes))
	for idx, raw := range result.Nodes {
		id := toString(raw["id"])
		if id == "" {
			id = fmt.Sprintf("kg_n_%d", idx+1)
		}
		data := toMap(raw["data"])
		label := firstNonEmpty(toString(raw["label"]), toString(data["label"]))
		if label == "" {
			label = id
		}
		entityType := firstNonEmpty(toString(raw["entityType"]), toString(data["entityType"]), "component")
		x, y := pickKGXY(raw)

		styleJSON, _ := marshalMaybe(raw["style"], []byte("{}"))
		dataExt := map[string]interface{}{}
		for k, v := range data {
			switch k {
			case "label", "entityType", "description", "sourceDoc":
				continue
			default:
				dataExt[k] = v
			}
		}
		dataExtJSON, _ := marshalMaybe(dataExt, []byte("{}"))

		n := model.KnowledgeGraphNode{
			ID:          id,
			Type:        firstNonEmpty(toString(raw["type"]), "entityNode"),
			PositionX:   x,
			PositionY:   y,
			Label:       label,
			EntityType:  entityType,
			StyleJson:   styleJSON,
			DataExtJson: dataExtJSON,
		}
		if v := firstNonEmpty(toString(raw["description"]), toString(data["description"])); v != "" {
			n.Description = ptr(v)
		}
		if v := firstNonEmpty(toString(raw["sourceDoc"]), toString(data["sourceDoc"])); v != "" {
			n.SourceDoc = ptr(v)
		}
		nodes = append(nodes, n)
	}

	edges := make([]model.KnowledgeGraphEdge, 0, len(result.Edges))
	for idx, raw := range result.Edges {
		source := firstNonEmpty(toString(raw["source"]), toString(raw["from"]))
		target := firstNonEmpty(toString(raw["target"]), toString(raw["to"]))
		if source == "" || target == "" {
			continue
		}
		id := toString(raw["id"])
		if id == "" {
			id = fmt.Sprintf("kg_e_%d", idx+1)
		}
		data := toMap(raw["data"])
		styleJSON, _ := marshalMaybe(raw["style"], []byte("{}"))
		labelStyleJSON, _ := marshalMaybe(raw["labelStyle"], []byte("{}"))
		labelBgStyleJSON, _ := marshalMaybe(raw["labelBgStyle"], []byte("{}"))

		e := model.KnowledgeGraphEdge{
			ID:               id,
			SourceNodeID:     source,
			TargetNodeID:     target,
			Type:             firstNonEmpty(toString(raw["type"]), "smoothstep"),
			Animated:         toBool(raw["animated"]),
			StyleJson:        styleJSON,
			LabelStyleJson:   labelStyleJSON,
			LabelBgStyleJson: labelBgStyleJSON,
		}
		if v := firstNonEmpty(toString(raw["label"]), toString(data["label"])); v != "" {
			e.Label = ptr(v)
		}
		edges = append(edges, e)
	}

	return nodes, edges, nil
}

func mapFaultTreeNodeType(rawType string, data map[string]interface{}) (string, *string) {
	t := strings.ToLower(strings.TrimSpace(rawType))
	nodeType := strings.ToLower(strings.TrimSpace(toString(data["nodeType"])))
	label := strings.ToUpper(strings.TrimSpace(toString(data["label"])))

	gate := func(v string) (string, *string) {
		if v == "" {
			return "gate", nil
		}
		up := strings.ToUpper(v)
		if up != "AND" && up != "OR" && up != "NOT" {
			return "gate", nil
		}
		return "gate", ptr(up)
	}

	switch t {
	case "topevent", "top_event":
		return "topEvent", nil
	case "midevent", "intermediateevent", "intermediate_event", "middleevent":
		return "midEvent", nil
	case "basicevent", "basic_event":
		return "basicEvent", nil
	case "gate", "gatenode":
		if label == "AND" || label == "OR" || label == "NOT" {
			return gate(label)
		}
		return gate("")
	case "andgate", "orgate", "notgate", "and", "or", "not":
		return gate(strings.ToUpper(strings.TrimSuffix(strings.TrimSuffix(t, "gate"), "node")))
	}

	switch nodeType {
	case "topevent", "top_event":
		return "topEvent", nil
	case "midevent", "intermediateevent", "intermediate_event":
		return "midEvent", nil
	case "basicevent", "basic_event":
		return "basicEvent", nil
	case "andgate":
		return gate("AND")
	case "orgate":
		return gate("OR")
	case "notgate":
		return gate("NOT")
	}
	if label == "AND" || label == "OR" || label == "NOT" {
		return gate(label)
	}
	return "midEvent", nil
}

func pickXY(raw map[string]interface{}) (float64, float64) {
	x := toFloat(raw["x"], 0)
	y := toFloat(raw["y"], 0)
	if pos := toMap(raw["position"]); len(pos) > 0 {
		if x == 0 {
			x = toFloat(pos["x"], 0)
		}
		if y == 0 {
			y = toFloat(pos["y"], 0)
		}
	}
	return x, y
}

func pickKGXY(raw map[string]interface{}) (float64, float64) {
	x := toFloat(raw["positionX"], 0)
	y := toFloat(raw["positionY"], 0)
	if pos := toMap(raw["position"]); len(pos) > 0 {
		if x == 0 {
			x = toFloat(pos["x"], 0)
		}
		if y == 0 {
			y = toFloat(pos["y"], 0)
		}
	}
	return x, y
}

func normalizeBoundedID(raw, prefix string, maxLen int) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		v = prefix
	}
	if len(v) <= maxLen {
		return v
	}
	sum := sha1.Sum([]byte(v))
	hash := hex.EncodeToString(sum[:])
	maxHashLen := maxLen - len(prefix) - 1
	if maxHashLen < 8 {
		maxHashLen = 8
	}
	if maxHashLen > len(hash) {
		maxHashLen = len(hash)
	}
	return prefix + "_" + hash[:maxHashLen]
}

func toMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return ""
	}
}

func toFloat(v interface{}, def float64) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, err := t.Float64()
		if err == nil {
			return f
		}
	case string:
		if t == "" {
			return def
		}
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%f", &f); err == nil {
			return f
		}
	}
	return def
}

func toBool(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		lower := strings.ToLower(strings.TrimSpace(t))
		return lower == "true" || lower == "1" || lower == "yes"
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return false
}

func pickFloatPtr(v interface{}) *float64 {
	f := toFloat(v, 0)
	switch v.(type) {
	case nil:
		return nil
	default:
		return &f
	}
}

func toStringArray(v interface{}) pq.StringArray {
	arr := pq.StringArray{}
	switch t := v.(type) {
	case []string:
		for _, s := range t {
			ts := strings.TrimSpace(s)
			if ts != "" {
				arr = append(arr, ts)
			}
		}
	case []interface{}:
		for _, item := range t {
			ts := toString(item)
			if ts != "" {
				arr = append(arr, ts)
			}
		}
	}
	return arr
}

func marshalMaybe(v interface{}, def []byte) ([]byte, bool) {
	if v == nil {
		return def, true
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 {
		return def, true
	}
	return b, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func ptr(s string) *string {
	v := strings.TrimSpace(s)
	if v == "" {
		return nil
	}
	return &v
}
