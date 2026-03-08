-- ============================================================
-- Migration: 000011_create_notification_audit_tables
-- Description: 通知与审计日志两表：
--   1. notifications —— 站内通知（邀请/版本/AI完成/系统公告）
--   2. audit_logs    —— 操作审计日志（用于"操作日志"入口）
-- ============================================================

-- ─── 1. 通知表 ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS notifications (
    id         VARCHAR(32)  NOT NULL,
    user_id    VARCHAR(32)  NOT NULL,

    -- 通知类型（对应不同来源）
    type       VARCHAR(50)  NOT NULL
                            CHECK (type IN (
                                'project_invite',       -- 项目邀请
                                'member_role_changed',  -- 成员角色变化
                                'version_created',      -- 版本创建
                                'ai_task_completed',    -- AI 任务完成
                                'ai_task_failed',       -- AI 任务失败
                                'system_announce'       -- 系统公告
                            )),
    title      VARCHAR(100) NOT NULL,
    content    TEXT,
    is_read    BOOLEAN      NOT NULL DEFAULT FALSE,

    -- 关联资源（可选：项目ID / 邀请ID / 任务ID 等）
    project_id  VARCHAR(32),
    resource_id VARCHAR(32),

    -- 扩展字段，用于存储通知来源的附加数据
    extra_json JSONB        NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_notifications       PRIMARY KEY (id),
    CONSTRAINT fk_notif_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  notifications             IS '站内通知表';
COMMENT ON COLUMN notifications.id          IS '通知ID，格式：notif_{timestamp}_{随机}';
COMMENT ON COLUMN notifications.type        IS '通知类型';
COMMENT ON COLUMN notifications.is_read     IS '是否已读';
COMMENT ON COLUMN notifications.resource_id IS '关联资源ID（邀请/版本/任务等）';
COMMENT ON COLUMN notifications.extra_json  IS '扩展数据（如项目名称、版本标签等）';


-- ─── 2. 操作审计日志表 ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_logs (
    id            VARCHAR(32)  NOT NULL,

    -- 操作人（删除后保留记录，置空）
    user_id       VARCHAR(32),
    -- 操作人快照名（冗余，防止用户被删后丢失可读信息）
    operator_name VARCHAR(30),

    -- 操作类型（如 "project.delete"、"member.invite"、"version.rollback"）
    action        VARCHAR(60)  NOT NULL,
    -- 操作对象类型（如 "project"、"version"、"member"）
    resource_type VARCHAR(30)  NOT NULL,
    -- 操作对象ID
    resource_id   VARCHAR(32),
    -- 操作摘要（可读文本）
    summary       TEXT,

    ip_address    INET,
    user_agent    TEXT,

    -- 关联项目（便于按项目筛选审计日志）
    project_id    VARCHAR(32),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_audit_logs       PRIMARY KEY (id),
    CONSTRAINT fk_al_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE SET NULL
);

COMMENT ON TABLE  audit_logs               IS '操作审计日志表，用于"操作日志"入口展示';
COMMENT ON COLUMN audit_logs.id            IS '日志ID，格式：log_{timestamp}_{随机}';
COMMENT ON COLUMN audit_logs.action        IS '操作类型，格式：{资源}.{动作}，如 project.delete';
COMMENT ON COLUMN audit_logs.resource_type IS '资源类型：project / version / member / graph 等';
COMMENT ON COLUMN audit_logs.operator_name IS '操作人展示名（冗余，防止用户删除后信息丢失）';
COMMENT ON COLUMN audit_logs.summary       IS '操作摘要（人类可读描述）';
