-- ============================================================
-- Migration: 000007_create_project_members
-- Description: 项目成员表 —— 协作权限管理（admin / editor / viewer）
-- ============================================================

CREATE TABLE IF NOT EXISTS project_members (
    id         VARCHAR(32) NOT NULL,
    project_id VARCHAR(32) NOT NULL,
    user_id    VARCHAR(32) NOT NULL,
    role       VARCHAR(10) NOT NULL
                           CHECK (role IN ('admin', 'editor', 'viewer')),
    status     VARCHAR(10) NOT NULL DEFAULT 'active'
                           CHECK (status IN ('active', 'inactive')),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_project_members           PRIMARY KEY (id),
    -- 同一用户在同一项目中只能有一条成员记录
    CONSTRAINT uq_project_members_user_proj UNIQUE (project_id, user_id),
    CONSTRAINT fk_pm_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_pm_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  project_members          IS '项目协作成员表';
COMMENT ON COLUMN project_members.id       IS '成员记录ID，格式：member_{timestamp}_{随机}';
COMMENT ON COLUMN project_members.role     IS '项目内角色：admin(管理员) / editor(编辑者) / viewer(查看者)';
COMMENT ON COLUMN project_members.status   IS '成员状态：active / inactive';
COMMENT ON COLUMN project_members.joined_at IS '加入时间';
