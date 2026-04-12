-- ============================================================
-- Migration: 000020_expand_agent_tool_call_statuses (down)
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
        'timeout'
    ));
