-- ============================================================
-- Migration: 000021_add_agent_tool_history_fields
-- Description: 为 ai_chat_messages 增加结构化 tool 回注持久化字段
-- ============================================================

ALTER TABLE ai_chat_messages
    DROP CONSTRAINT IF EXISTS ai_chat_messages_role_check;

ALTER TABLE ai_chat_messages
    ADD CONSTRAINT ai_chat_messages_role_check
    CHECK (role IN ('user', 'assistant', 'system', 'tool'));

ALTER TABLE ai_chat_messages
    ADD COLUMN IF NOT EXISTS reasoning_content TEXT,
    ADD COLUMN IF NOT EXISTS tool_calls JSONB,
    ADD COLUMN IF NOT EXISTS tool_call_id VARCHAR(64);

COMMENT ON COLUMN ai_chat_messages.reasoning_content IS 'assistant 消息的 reasoning 内容（可选）';
COMMENT ON COLUMN ai_chat_messages.tool_calls IS 'assistant 消息中的结构化 tool_calls（JSON 数组）';
COMMENT ON COLUMN ai_chat_messages.tool_call_id IS 'tool 角色消息关联的 tool call id';
