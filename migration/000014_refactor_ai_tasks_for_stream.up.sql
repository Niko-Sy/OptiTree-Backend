-- ============================================================
-- Migration: 000014_refactor_ai_tasks_for_stream
-- Description: AI 任务表切换到 Redis Stream + Worker 回调模型
-- ============================================================

-- 清理旧任务类型记录（旧方案不再兼容）
DELETE FROM ai_tasks
WHERE type NOT IN ('generateFaultTree', 'generateKnowledgeGraph');

-- 先移除旧 CHECK，再做状态值迁移
ALTER TABLE ai_tasks DROP CONSTRAINT IF EXISTS ai_tasks_status_check;
ALTER TABLE ai_tasks DROP CONSTRAINT IF EXISTS ai_tasks_type_check;

-- 状态迁移到新状态机
UPDATE ai_tasks
SET status = 'processing'
WHERE status IN ('parsing', 'generating');

UPDATE ai_tasks
SET status = 'dead'
WHERE status IN ('cancelled');

-- 扩展字段
ALTER TABLE ai_tasks
    ADD COLUMN IF NOT EXISTS attempt_count  INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS max_attempts   INT         NOT NULL DEFAULT 3,
    ADD COLUMN IF NOT EXISTS worker_id      VARCHAR(64),
    ADD COLUMN IF NOT EXISTS queued_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS started_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS completed_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(64);

UPDATE ai_tasks
SET queued_at = created_at
WHERE queued_at IS NULL;

-- 新约束：仅保留生成任务，状态收敛为新状态机
ALTER TABLE ai_tasks
    ADD CONSTRAINT ai_tasks_type_check
    CHECK (type IN ('generateFaultTree', 'generateKnowledgeGraph'));

ALTER TABLE ai_tasks
    ADD CONSTRAINT ai_tasks_status_check
    CHECK (status IN ('pending', 'processing', 'retrying', 'completed', 'failed', 'dead'));

-- 索引
CREATE INDEX IF NOT EXISTS idx_ai_tasks_status_updated_at
    ON ai_tasks(status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_ai_tasks_worker_id
    ON ai_tasks(worker_id);

CREATE UNIQUE INDEX IF NOT EXISTS uq_ai_tasks_idempotency_key
    ON ai_tasks(idempotency_key)
    WHERE idempotency_key IS NOT NULL;

COMMENT ON COLUMN ai_tasks.attempt_count   IS '当前已尝试次数';
COMMENT ON COLUMN ai_tasks.max_attempts    IS '最大重试次数';
COMMENT ON COLUMN ai_tasks.worker_id       IS '当前或最后执行该任务的 Worker 标识';
COMMENT ON COLUMN ai_tasks.queued_at       IS '入队时间';
COMMENT ON COLUMN ai_tasks.started_at      IS '首次开始处理时间';
COMMENT ON COLUMN ai_tasks.completed_at    IS '完成或最终失败时间';
COMMENT ON COLUMN ai_tasks.idempotency_key IS '任务幂等键';
