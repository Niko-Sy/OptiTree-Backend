-- ============================================================
-- Migration: 000017_optimize_project_list_indexes (down)
-- ============================================================

DROP INDEX IF EXISTS idx_projects_created_by_type_updated_at;
DROP INDEX IF EXISTS idx_pm_project_status_user;
DROP INDEX IF EXISTS idx_pm_user_status_project;
