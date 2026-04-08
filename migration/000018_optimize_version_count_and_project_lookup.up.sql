-- ============================================================
-- Migration: 000018_optimize_version_count_and_project_lookup
-- Description: optimize version count query and reinforce project lookup path
-- ============================================================

-- Fast path for COUNT(*) WHERE project_id = ?
CREATE INDEX IF NOT EXISTS idx_vs_project_id_count
    ON version_snapshots (project_id);

-- NOTE: projects(id) is already indexed by PRIMARY KEY.
-- Keep a dedicated lookup index for environments where historical schema drift exists.
CREATE INDEX IF NOT EXISTS idx_projects_id_lookup
    ON projects (id);
