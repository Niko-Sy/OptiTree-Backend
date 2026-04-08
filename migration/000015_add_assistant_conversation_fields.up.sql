-- ============================================================
-- Migration: 000015_add_assistant_conversation_fields
-- Description: Assistant 会话域增强字段与索引
-- ============================================================

ALTER TABLE ai_conversations
    ADD COLUMN IF NOT EXISTS title VARCHAR(120),
    ADD COLUMN IF NOT EXISTS last_message_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS message_count INT NOT NULL DEFAULT 0;

ALTER TABLE ai_chat_messages
    ADD COLUMN IF NOT EXISTS model VARCHAR(80),
    ADD COLUMN IF NOT EXISTS tokens_used INT,
    ADD COLUMN IF NOT EXISTS is_partial BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_aconv_user_last_msg
    ON ai_conversations (user_id, last_message_at DESC, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_aconv_user_project_type
    ON ai_conversations (user_id, project_id, type, updated_at DESC);

COMMENT ON COLUMN ai_conversations.title IS '会话标题，默认取首条用户消息截断';
COMMENT ON COLUMN ai_conversations.last_message_at IS '最近一条消息时间';
COMMENT ON COLUMN ai_conversations.message_count IS '会话消息总数';
COMMENT ON COLUMN ai_chat_messages.model IS '本条消息对应模型（assistant消息可记录）';
COMMENT ON COLUMN ai_chat_messages.tokens_used IS '本条响应消耗 token（如可获得）';
COMMENT ON COLUMN ai_chat_messages.is_partial IS '是否为流式中断导致的不完整消息';
