-- ============================================================
-- Migration: 000001_create_users
-- Description: 用户表 —— 系统所有认证与权限的起点
-- ============================================================

CREATE TABLE IF NOT EXISTS users (
    id            VARCHAR(32)  NOT NULL,
    username      VARCHAR(20)  NOT NULL,
    display_name  VARCHAR(30)  NOT NULL,
    email         VARCHAR(100) NOT NULL,
    password_hash TEXT         NOT NULL,
    avatar        TEXT,
    role          VARCHAR(10)  NOT NULL DEFAULT 'user'
                               CHECK (role IN ('user', 'admin')),
    status        VARCHAR(10)  NOT NULL DEFAULT 'active'
                               CHECK (status IN ('active', 'inactive', 'banned')),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ,

    CONSTRAINT pk_users PRIMARY KEY (id),
    CONSTRAINT uq_users_username UNIQUE (username),
    CONSTRAINT uq_users_email    UNIQUE (email)
);

COMMENT ON TABLE  users              IS '用户主表';
COMMENT ON COLUMN users.id           IS '用户ID，业务生成，格式：user_{timestamp}_{随机}';
COMMENT ON COLUMN users.username     IS '用户名，3-20位，允许字母/数字/下划线/中文，全局唯一';
COMMENT ON COLUMN users.display_name IS '显示名称，最长30字';
COMMENT ON COLUMN users.email        IS '邮箱，合法格式，全局唯一';
COMMENT ON COLUMN users.password_hash IS 'bcrypt哈希后的密码，原始密码不落库';
COMMENT ON COLUMN users.avatar       IS '头像URL（CDN地址）';
COMMENT ON COLUMN users.role         IS '系统角色：user / admin';
COMMENT ON COLUMN users.status       IS '账号状态：active / inactive / banned';
COMMENT ON COLUMN users.last_login_at IS '最近一次登录时间';
