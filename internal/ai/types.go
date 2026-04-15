package ai

import "encoding/json"

// ToolCall represents a single tool call returned by the LLM.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolCallResult is the result of executing a tool call, fed back to the LLM.
type ToolCallResult struct {
	CallID  string `json:"callId"`
	Content string `json:"content"`
}

// AgentChatRequest extends ChatRequest with tool definitions for Agent mode.
type AgentChatRequest struct {
	ChatRequest
	Tools                []OAIToolDef `json:"tools,omitempty"`
	ToolChoice           string       `json:"toolChoice,omitempty"` // "auto" | "none"
	ToolGuide            string       `json:"toolGuide,omitempty"`
	Temperature          *float64     `json:"temperature,omitempty"`
	EnableFallbackParser bool         `json:"enableFallbackParser,omitempty"`
}

// AgentChatResponse is the response from ChatWithTools.
type AgentChatResponse struct {
	Reply            string     `json:"reply"`
	ReasoningContent string     `json:"reasoningContent,omitempty"`
	ToolCalls        []ToolCall `json:"toolCalls,omitempty"`
	TokensUsed       int        `json:"tokensUsed"`
	ModelUsed        string     `json:"modelUsed"`
}

// OAIToolDef follows the OpenAI function calling format for tool definitions.
type OAIToolDef struct {
	Type     string         `json:"type"` // "function"
	Function OAIToolFuncDef `json:"function"`
}

// OAIToolFuncDef describes a single tool function.
type OAIToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// oaiToolCall is the wire format for a tool call in the OpenAI response.
type oaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// oaiStreamToolCallDelta is the wire format for tool call deltas in streaming.
type oaiStreamToolCallDelta struct {
	Index    int     `json:"index"`
	ID       *string `json:"id,omitempty"`
	Type     *string `json:"type,omitempty"`
	Function *struct {
		Name      *string `json:"name,omitempty"`
		Arguments *string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}
