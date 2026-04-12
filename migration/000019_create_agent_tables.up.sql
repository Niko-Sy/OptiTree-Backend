-- ============================================================
-- Migration: 000019_create_agent_tables
-- Description: Agent 会话与工具调用审计表
-- ============================================================

-- ─── 1. Agent 会话表 ──────────────────────────────────────────
CREATE TABLE IF NOT EXISTS agent_sessions (
    id               VARCHAR(32)  NOT NULL,
    conversation_id  VARCHAR(32)  NOT NULL,
    project_id       VARCHAR(32)  NOT NULL,
    user_id          VARCHAR(32)  NOT NULL,
    graph_type       VARCHAR(20)  NOT NULL
                                CHECK (graph_type IN ('faultTree', 'knowledgeGraph')),
    state            VARCHAR(20)  NOT NULL DEFAULT 'running'
                                CHECK (state IN (
                                    'running',
                                    'paused_confirm',
                                    'paused_preview',
                                    'done',
                                    'cancelled',
                                    'failed',
                                    'timeout'
                                )),
    tool_call_count  INT          NOT NULL DEFAULT 0,
    server_ops       INT          NOT NULL DEFAULT 0,
    client_ops       INT          NOT NULL DEFAULT 0,
    hybrid_ops       INT          NOT NULL DEFAULT 0,
    tokens_used      INT          NOT NULL DEFAULT 0,
    error_message    TEXT,
    started_at       TIMESTAMPTZ  NOT NULL,
    ended_at         TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_agent_sessions PRIMARY KEY (id),
    CONSTRAINT fk_agent_session_conversation FOREIGN KEY (conversation_id)
        REFERENCES ai_conversations(id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_session_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_session_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_conversation
    ON agent_sessions (conversation_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_project_state
    ON agent_sessions (project_id, state, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_user_created
    ON agent_sessions (user_id, created_at DESC);

COMMENT ON TABLE agent_sessions IS 'Agent 会话记录（用于断线恢复与审计）';
COMMENT ON COLUMN agent_sessions.state IS '会话状态：running/paused_confirm/paused_preview/done/cancelled/failed/timeout';

-- ─── 2. Agent 工具调用表 ──────────────────────────────────────
CREATE TABLE IF NOT EXISTS agent_tool_calls (
    id            VARCHAR(32)  NOT NULL,
    session_id    VARCHAR(32)  NOT NULL,
    call_id       VARCHAR(64)  NOT NULL,
    tool_name     VARCHAR(64)  NOT NULL,
    tier          VARCHAR(10)  NOT NULL
                              CHECK (tier IN ('server', 'client', 'hybrid')),
    arguments     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    result        TEXT,
    patch_json    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    status        VARCHAR(20)  NOT NULL DEFAULT 'pending'
                              CHECK (status IN (
                                  'pending',
                                  'running',
                                  'success',
                                  'failed',
                                  'cancelled',
                                  'timeout'
                              )),
    error_msg     TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ,

    CONSTRAINT pk_agent_tool_calls PRIMARY KEY (id),
    CONSTRAINT uq_agent_tool_calls_session_call UNIQUE (session_id, call_id),
    CONSTRAINT fk_agent_tool_calls_session FOREIGN KEY (session_id)
        REFERENCES agent_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_tool_calls_session_created
    ON agent_tool_calls (session_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_tool_calls_tool_status
    ON agent_tool_calls (tool_name, status, created_at DESC);

COMMENT ON TABLE agent_tool_calls IS 'Agent 工具调用审计表';
COMMENT ON COLUMN agent_tool_calls.patch_json IS '工具调用产生的图变更 patch';
