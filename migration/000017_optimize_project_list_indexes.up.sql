-- ============================================================
-- Migration: 000017_optimize_project_list_indexes
-- Description: optimize project list access path
-- ============================================================

-- Accelerate membership subquery/existence check by user and active status.
CREATE INDEX IF NOT EXISTS idx_pm_user_status_project
    ON project_members (user_id, status, project_id);

-- Accelerate member listing and invalidation paths by project.
CREATE INDEX IF NOT EXISTS idx_pm_project_status_user
    ON project_members (project_id, status, user_id);

-- Accelerate owner-side project list filtering/sorting when type is specified.
CREATE INDEX IF NOT EXISTS idx_projects_created_by_type_updated_at
    ON projects (created_by, type, updated_at DESC);


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
