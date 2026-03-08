-- ============================================================
-- Migration: 000010_create_auth_support_tables
-- Description: 认证支撑三表：
--   1. refresh_tokens       —— Refresh Token 持久化（Redis 主存，DB 备份）
--   2. login_logs           —— 登录记录（用于用户中心展示）
--   3. user_social_bindings —— OAuth 社交账号绑定（GitHub / 微信 / QQ）
-- ============================================================

-- ─── 1. Refresh Token 表 ──────────────────────────────────────
-- 注意：日常认证以 Redis 为主，本表用于 Redis 失效后的兜底校验
--       以及 "记住我" 场景下的长期 Token 持久化
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id           VARCHAR(32)  NOT NULL,
    user_id      VARCHAR(32)  NOT NULL,
    -- bcrypt 哈希后的 Token（原始 Token 不落库）
    token_hash   TEXT         NOT NULL,
    expires_at   TIMESTAMPTZ  NOT NULL,
    -- 是否已撤销（登出/强制下线后标记）
    is_revoked   BOOLEAN      NOT NULL DEFAULT FALSE,
    -- 来自哪种客户端，便于多设备管理
    user_agent   TEXT,
    ip_address   INET,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_refresh_tokens           PRIMARY KEY (id),
    CONSTRAINT uq_refresh_tokens_hash      UNIQUE (token_hash),
    CONSTRAINT fk_rt_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  refresh_tokens              IS 'Refresh Token 持久化表';
COMMENT ON COLUMN refresh_tokens.token_hash   IS 'Token的bcrypt哈希，原始值不落库';
COMMENT ON COLUMN refresh_tokens.is_revoked   IS '是否已撤销（登出/踢出设备后为 true）';


-- ─── 2. 登录记录表 ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS login_logs (
    id          VARCHAR(32)  NOT NULL,
    user_id     VARCHAR(32)  NOT NULL,
    -- 登录结果：true=成功, false=失败
    success     BOOLEAN      NOT NULL DEFAULT TRUE,
    ip_address  INET,
    -- 地理位置文本（如 "中国·上海"）
    region      VARCHAR(100),
    -- 操作系统 + 浏览器摘要（由 user-agent 解析）
    device_info VARCHAR(200),
    user_agent  TEXT,
    -- 失败原因（如 "密码错误"、"账号已封禁"）
    fail_reason VARCHAR(100),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_login_logs PRIMARY KEY (id),
    CONSTRAINT fk_ll_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  login_logs             IS '登录记录表，用于用户中心"登录记录"展示';
COMMENT ON COLUMN login_logs.ip_address  IS '登录IP，使用 PostgreSQL INET 类型';
COMMENT ON COLUMN login_logs.region      IS '地理位置（IP解析结果）';
COMMENT ON COLUMN login_logs.device_info IS '设备摘要（从 User-Agent 解析）';
COMMENT ON COLUMN login_logs.fail_reason IS '登录失败原因';


-- ─── 3. 社交账号绑定表 ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_social_bindings (
    id               VARCHAR(32)  NOT NULL,
    user_id          VARCHAR(32)  NOT NULL,
    -- 平台名称
    provider         VARCHAR(20)  NOT NULL
                                  CHECK (provider IN ('github', 'wechat', 'qq')),
    -- 平台侧的用户唯一ID
    provider_user_id VARCHAR(100) NOT NULL,
    -- 平台侧昵称（可选，用于展示）
    provider_name    VARCHAR(100),
    -- 平台侧头像（可选）
    provider_avatar  TEXT,
    -- 加密存储的 Access Token（如无需服务端调用平台 API 可置空）
    access_token     TEXT,
    refresh_token    TEXT,
    expires_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_user_social_bindings               PRIMARY KEY (id),
    -- 同一平台的同一账号只能绑定给一个系统用户
    CONSTRAINT uq_usb_provider_uid UNIQUE (provider, provider_user_id),
    -- 同一用户在同一平台只能绑定一次
    CONSTRAINT uq_usb_user_provider UNIQUE (user_id, provider),
    CONSTRAINT fk_usb_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  user_social_bindings                  IS 'OAuth 社交账号绑定表';
COMMENT ON COLUMN user_social_bindings.provider         IS '平台：github / wechat / qq';
COMMENT ON COLUMN user_social_bindings.provider_user_id IS '平台侧用户唯一标识';
COMMENT ON COLUMN user_social_bindings.access_token     IS '平台 Access Token（加密存储）';
