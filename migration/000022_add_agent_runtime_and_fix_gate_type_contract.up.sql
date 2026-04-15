-- ============================================================
-- Migration: 000022_add_agent_runtime_and_fix_gate_type_contract
-- Description:
--   1) 新增 agent_session_runtime，持久化 pending confirm/preview/resume 状态。
--   2) 统一 fault_tree_nodes.gate_type 约束为 AND/OR/NOT。
--   3) 兼容性兜底：将历史非法 VOTE 数据迁移为 OR。
-- ============================================================

CREATE TABLE IF NOT EXISTS agent_session_runtime (
    session_id       VARCHAR(32)  NOT NULL,
    pending_call_id  VARCHAR(64),
    pending_tool_name VARCHAR(64),
    pending_tier     VARCHAR(16),
    pending_args     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    pending_preview  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    wait_type        VARCHAR(20)  NOT NULL DEFAULT 'none'
                               CHECK (wait_type IN ('none', 'confirm', 'preview', 'iteration')),
    wait_status      VARCHAR(20)  NOT NULL DEFAULT 'cleared'
                               CHECK (wait_status IN ('waiting', 'approved', 'rejected', 'timeout', 'cleared')),
    last_event_seq   BIGINT       NOT NULL DEFAULT 0,
    expires_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_agent_session_runtime PRIMARY KEY (session_id),
    CONSTRAINT fk_agent_session_runtime_session FOREIGN KEY (session_id)
        REFERENCES agent_sessions(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_agent_session_runtime_wait_status
    ON agent_session_runtime (wait_status, wait_type, updated_at DESC);

COMMENT ON TABLE agent_session_runtime IS 'Agent 会话运行时状态（pending confirm/preview/resume）';
COMMENT ON COLUMN agent_session_runtime.pending_args IS '待确认调用参数（JSON）';
COMMENT ON COLUMN agent_session_runtime.pending_preview IS '待确认预览内容（JSON）';

-- 兼容历史脏数据：统一将 VOTE 迁移为 OR。
UPDATE fault_tree_nodes
   SET gate_type = 'OR'
 WHERE UPPER(COALESCE(gate_type, '')) = 'VOTE';

DO $$
DECLARE c RECORD;
BEGIN
  FOR c IN
      SELECT conname
        FROM pg_constraint
       WHERE conrelid = 'fault_tree_nodes'::regclass
         AND contype = 'c'
         AND pg_get_constraintdef(oid) ILIKE '%gate_type%'
  LOOP
    EXECUTE format('ALTER TABLE fault_tree_nodes DROP CONSTRAINT IF EXISTS %I', c.conname);
  END LOOP;
END $$;

ALTER TABLE fault_tree_nodes
    ADD CONSTRAINT fault_tree_nodes_gate_type_check
    CHECK (gate_type IS NULL OR gate_type IN ('AND', 'OR', 'NOT'));