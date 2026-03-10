-- ============================================================
-- Migration: 000013_add_project_generation_status
-- Description: 为 projects 增加 AI 生成状态字段
-- ============================================================

ALTER TABLE projects
    ADD COLUMN IF NOT EXISTS generation_status VARCHAR(32)
    CHECK (generation_status IN ('pending_generating', 'generating', 'completed', 'failed'));

COMMENT ON COLUMN projects.generation_status IS 'AI 生成状态：pending_generating / generating / completed / failed';
