package model

import (
	"time"

	"gorm.io/datatypes"
)

// AgentSession persists one streaming agent run for audit and reconnect.
type AgentSession struct {
	ID             string     `gorm:"primaryKey;column:id;size:32" json:"id"`
	ConversationID string     `gorm:"column:conversation_id;size:32;not null;index" json:"conversationId"`
	ProjectID      string     `gorm:"column:project_id;size:32;not null;index" json:"projectId"`
	UserID         string     `gorm:"column:user_id;size:32;not null;index" json:"userId"`
	GraphType      string     `gorm:"column:graph_type;size:20;not null" json:"graphType"`
	State          string     `gorm:"column:state;size:20;not null;default:'running'" json:"state"`
	ToolCallCount  int        `gorm:"column:tool_call_count;not null;default:0" json:"toolCallCount"`
	ServerOps      int        `gorm:"column:server_ops;not null;default:0" json:"serverOps"`
	ClientOps      int        `gorm:"column:client_ops;not null;default:0" json:"clientOps"`
	HybridOps      int        `gorm:"column:hybrid_ops;not null;default:0" json:"hybridOps"`
	TokensUsed     int        `gorm:"column:tokens_used;not null;default:0" json:"tokensUsed"`
	ErrorMessage   *string    `gorm:"column:error_message;type:text" json:"errorMessage,omitempty"`
	StartedAt      time.Time  `gorm:"column:started_at;not null" json:"startedAt"`
	EndedAt        *time.Time `gorm:"column:ended_at" json:"endedAt,omitempty"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
}

func (AgentSession) TableName() string {
	return "agent_sessions"
}

// AgentToolCall persists one tool invocation inside an agent session.
type AgentToolCall struct {
	ID         string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	SessionID  string         `gorm:"column:session_id;size:32;not null;index" json:"sessionId"`
	CallID     string         `gorm:"column:call_id;size:64;not null" json:"callId"`
	ToolName   string         `gorm:"column:tool_name;size:64;not null" json:"toolName"`
	Tier       string         `gorm:"column:tier;size:10;not null" json:"tier"`
	Arguments  datatypes.JSON `gorm:"column:arguments;type:jsonb" json:"arguments"`
	Result     *string        `gorm:"column:result;type:text" json:"result,omitempty"`
	PatchJSON  datatypes.JSON `gorm:"column:patch_json;type:jsonb" json:"patchJson"`
	Status     string         `gorm:"column:status;size:20;not null;default:'pending'" json:"status"`
	ErrorMsg   *string        `gorm:"column:error_msg;type:text" json:"errorMsg,omitempty"`
	CreatedAt  time.Time      `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	FinishedAt *time.Time     `gorm:"column:finished_at" json:"finishedAt,omitempty"`
}

func (AgentToolCall) TableName() string {
	return "agent_tool_calls"
}

// AgentSessionRuntime stores resumable pending state for a session.
type AgentSessionRuntime struct {
	SessionID      string         `gorm:"primaryKey;column:session_id;size:32" json:"sessionId"`
	PendingCallID  *string        `gorm:"column:pending_call_id;size:64" json:"pendingCallId,omitempty"`
	PendingTool    *string        `gorm:"column:pending_tool_name;size:64" json:"pendingTool,omitempty"`
	PendingTier    *string        `gorm:"column:pending_tier;size:16" json:"pendingTier,omitempty"`
	PendingArgs    datatypes.JSON `gorm:"column:pending_args;type:jsonb" json:"pendingArgs,omitempty"`
	PendingPreview datatypes.JSON `gorm:"column:pending_preview;type:jsonb" json:"pendingPreview,omitempty"`
	WaitType       string         `gorm:"column:wait_type;size:20;not null;default:'none'" json:"waitType"`
	WaitStatus     string         `gorm:"column:wait_status;size:20;not null;default:'cleared'" json:"waitStatus"`
	LastEventSeq   int64          `gorm:"column:last_event_seq;not null;default:0" json:"lastEventSeq"`
	ExpiresAt      *time.Time     `gorm:"column:expires_at" json:"expiresAt,omitempty"`
	CreatedAt      time.Time      `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
}

func (AgentSessionRuntime) TableName() string {
	return "agent_session_runtime"
}
