-- ============================================================
-- Migration: 000016_add_pending_invitation_unique_index
-- Description: 同一项目同一邮箱仅允许一条 pending 邀请
-- ============================================================

CREATE UNIQUE INDEX IF NOT EXISTS uq_inv_project_email_pending
    ON invitations (project_id, lower(email))
    WHERE status = 'pending';
