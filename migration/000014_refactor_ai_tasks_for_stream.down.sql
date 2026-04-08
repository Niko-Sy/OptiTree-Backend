-- ============================================================
-- Migration: 000014_refactor_ai_tasks_for_stream (down)
-- ============================================================

DROP INDEX IF EXISTS uq_ai_tasks_idempotency_key;
DROP INDEX IF EXISTS idx_ai_tasks_worker_id;
DROP INDEX IF EXISTS idx_ai_tasks_status_updated_at;

ALTER TABLE ai_tasks DROP CONSTRAINT IF EXISTS ai_tasks_status_check;
ALTER TABLE ai_tasks DROP CONSTRAINT IF EXISTS ai_tasks_type_check;

-- 状态回滚到旧枚举
UPDATE ai_tasks
SET status = 'generating'
WHERE status IN ('processing', 'retrying');

UPDATE ai_tasks
SET status = 'failed'
WHERE status IN ('dead');

ALTER TABLE ai_tasks
    ADD CONSTRAINT ai_tasks_type_check
    CHECK (type IN (
        'generateFaultTree',
        'generateKnowledgeGraph',
        'parseDocument',
        'validateGraph'
    ));

ALTER TABLE ai_tasks
    ADD CONSTRAINT ai_tasks_status_check
    CHECK (status IN (
        'pending', 'parsing', 'generating',
        'completed', 'failed', 'cancelled'
    ));

ALTER TABLE ai_tasks
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS max_attempts,
    DROP COLUMN IF EXISTS worker_id,
    DROP COLUMN IF EXISTS queued_at,
    DROP COLUMN IF EXISTS started_at,
    DROP COLUMN IF EXISTS completed_at,
    DROP COLUMN IF EXISTS idempotency_key;
