package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var fallbackToolNameRegexp = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

// ParseFallbackToolCalls parses text-style tool invocations in the form:
// FUNCTION_CALL: tool_name({"arg":"value"})
func ParseFallbackToolCalls(raw string) ([]ToolCall, string) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	calls := make([]ToolCall, 0)
	keptLines := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		call, ok := parseFallbackToolCallLine(trimmed, len(calls)+1)
		if !ok {
			keptLines = append(keptLines, line)
			continue
		}
		calls = append(calls, call)
	}

	cleanReply := strings.TrimSpace(strings.Join(keptLines, "\n"))
	return calls, cleanReply
}

func parseFallbackToolCallLine(line string, idx int) (ToolCall, bool) {
	const prefix = "FUNCTION_CALL:"
	if !strings.HasPrefix(strings.ToUpper(line), prefix) {
		return ToolCall{}, false
	}

	payload := strings.TrimSpace(line[len(prefix):])
	if payload == "" {
		return ToolCall{}, false
	}

	leftParen := strings.Index(payload, "(")
	rightParen := strings.LastIndex(payload, ")")
	if leftParen <= 0 || rightParen <= leftParen {
		return ToolCall{}, false
	}

	name := strings.TrimSpace(payload[:leftParen])
	if !fallbackToolNameRegexp.MatchString(name) {
		return ToolCall{}, false
	}

	argsRaw := strings.TrimSpace(payload[leftParen+1 : rightParen])
	if argsRaw == "" {
		argsRaw = "{}"
	}

	return ToolCall{
		ID:        fmt.Sprintf("fallback_call_%d", idx),
		Name:      name,
		Arguments: normalizeToolArguments(argsRaw),
	}, true
}

func normalizeToolArguments(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage("{}")
	}

	candidates := []string{trimmed}
	if strings.HasPrefix(trimmed, "```") {
		cleaned := strings.TrimSpace(strings.TrimPrefix(trimmed, "```json"))
		cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, "```"))
		cleaned = strings.TrimSpace(strings.TrimSuffix(cleaned, "```"))
		if cleaned != "" {
			candidates = append(candidates, cleaned)
		}
	}

	if start := strings.Index(trimmed, "{"); start >= 0 {
		if end := strings.LastIndex(trimmed, "}"); end > start {
			candidates = append(candidates, strings.TrimSpace(trimmed[start:end+1]))
		}
	}
	if start := strings.Index(trimmed, "["); start >= 0 {
		if end := strings.LastIndex(trimmed, "]"); end > start {
			candidates = append(candidates, strings.TrimSpace(trimmed[start:end+1]))
		}
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if json.Valid([]byte(candidate)) {
			return json.RawMessage(candidate)
		}
	}

	encoded, err := json.Marshal(trimmed)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(encoded)
}
