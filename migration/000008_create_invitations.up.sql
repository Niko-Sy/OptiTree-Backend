-- ============================================================
-- Migration: 000008_create_invitations
-- Description: 项目邀请表 —— 邮件邀请流程（含接受/拒绝/过期状态）
-- ============================================================

CREATE TABLE IF NOT EXISTS invitations (
    id         VARCHAR(32)  NOT NULL,
    project_id VARCHAR(32)  NOT NULL,

    -- 受邀人邮箱（发送邀请时对方不一定已注册）
    email      VARCHAR(100) NOT NULL,
    role       VARCHAR(10)  NOT NULL
                            CHECK (role IN ('admin', 'editor', 'viewer')),
    status     VARCHAR(20)  NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'accepted', 'rejected', 'expired')),
    invited_by VARCHAR(32)  NOT NULL,

    -- 邮件中携带的一次性 token，sha256 安全随机值
    token      VARCHAR(128) NOT NULL,
    expires_at TIMESTAMPTZ  NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_invitations          PRIMARY KEY (id),
    -- token 全局唯一，防止碰撞
    CONSTRAINT uq_invitations_token    UNIQUE (token),
    CONSTRAINT fk_inv_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_inv_inviter FOREIGN KEY (invited_by)
        REFERENCES users(id) ON DELETE RESTRICT
);

COMMENT ON TABLE  invitations            IS '项目邀请表';
COMMENT ON COLUMN invitations.id         IS '邀请记录ID，格式：invite_{timestamp}_{随机}';
COMMENT ON COLUMN invitations.email      IS '受邀人邮箱，可能尚未注册';
COMMENT ON COLUMN invitations.role       IS '邀请角色：admin / editor / viewer';
COMMENT ON COLUMN invitations.status     IS '邀请状态：pending / accepted / rejected / expired';
COMMENT ON COLUMN invitations.token      IS '邮件链接中的一次性令牌，SHA256随机，全局唯一';
COMMENT ON COLUMN invitations.expires_at IS '邀请过期时间，建议24-72小时';
