-- ============================================================
-- Migration: 000021_add_agent_tool_history_fields (down)
-- ============================================================

ALTER TABLE ai_chat_messages
    DROP COLUMN IF EXISTS reasoning_content,
    DROP COLUMN IF EXISTS tool_calls,
    DROP COLUMN IF EXISTS tool_call_id;

ALTER TABLE ai_chat_messages
    DROP CONSTRAINT IF EXISTS ai_chat_messages_role_check;

ALTER TABLE ai_chat_messages
    ADD CONSTRAINT ai_chat_messages_role_check
    CHECK (role IN ('user', 'assistant', 'system'));
