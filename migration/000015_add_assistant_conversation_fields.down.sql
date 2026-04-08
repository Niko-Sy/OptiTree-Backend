-- ============================================================
-- Migration: 000015_add_assistant_conversation_fields (down)
-- ============================================================

DROP INDEX IF EXISTS idx_aconv_user_project_type;
DROP INDEX IF EXISTS idx_aconv_user_last_msg;

ALTER TABLE ai_chat_messages
    DROP COLUMN IF EXISTS is_partial,
    DROP COLUMN IF EXISTS tokens_used,
    DROP COLUMN IF EXISTS model;

ALTER TABLE ai_conversations
    DROP COLUMN IF EXISTS message_count,
    DROP COLUMN IF EXISTS last_message_at,
    DROP COLUMN IF EXISTS title;
