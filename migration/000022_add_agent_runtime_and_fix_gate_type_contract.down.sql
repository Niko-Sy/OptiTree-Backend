-- ============================================================
-- Migration: 000022_add_agent_runtime_and_fix_gate_type_contract (down)
-- ============================================================

DROP TABLE IF EXISTS agent_session_runtime;

ALTER TABLE fault_tree_nodes
    DROP CONSTRAINT IF EXISTS fault_tree_nodes_gate_type_check;

-- down 时仅放宽约束，不回滚数据值。
ALTER TABLE fault_tree_nodes
    ADD CONSTRAINT fault_tree_nodes_gate_type_check
    CHECK (gate_type IS NULL OR gate_type IN ('AND', 'OR', 'NOT', 'VOTE'));