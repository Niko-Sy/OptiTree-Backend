-- ============================================================
-- Migration: 000020_expand_agent_tool_call_statuses
-- Description: 扩展 agent_tool_calls.status 语义，区分 cancelled/discarded/client_only
-- ============================================================

ALTER TABLE agent_tool_calls
    DROP CONSTRAINT IF EXISTS agent_tool_calls_status_check;

ALTER TABLE agent_tool_calls
    ADD CONSTRAINT agent_tool_calls_status_check
    CHECK (status IN (
        'pending',
        'running',
        'success',
        'failed',
        'cancelled',
        'discarded',
        'client_only',
        'timeout'
    ));
