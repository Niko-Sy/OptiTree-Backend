-- ============================================================
-- Migration: 000018_optimize_version_count_and_project_lookup (down)
-- ============================================================

DROP INDEX IF EXISTS idx_projects_id_lookup;
DROP INDEX IF EXISTS idx_vs_project_id_count;
