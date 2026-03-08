-- ============================================================
-- Migration: 000009_create_ai_tables
-- Description: AI 相关三表：
--   1. ai_tasks          —— 异步任务（文档解析 / AI 生成 / 校验）
--   2. ai_conversations  —— AI 对话会话（按项目归属）
--   3. ai_chat_messages  —— 对话消息记录
-- ============================================================

-- ─── 1. AI 异步任务表 ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ai_tasks (
    id            VARCHAR(32)  NOT NULL,
    -- 任务类型
    type          VARCHAR(40)  NOT NULL
                               CHECK (type IN (
                                   'generateFaultTree',
                                   'generateKnowledgeGraph',
                                   'parseDocument',
                                   'validateGraph'
                               )),
    status        VARCHAR(20)  NOT NULL DEFAULT 'pending'
                               CHECK (status IN (
                                   'pending', 'parsing', 'generating',
                                   'completed', 'failed', 'cancelled'
                               )),
    -- 进度百分比 [0, 100]
    progress      INT          NOT NULL DEFAULT 0
                               CHECK (progress >= 0 AND progress <= 100),
    -- 当前阶段机器码（如 "generating"）
    stage         VARCHAR(30),
    -- 当前阶段展示文案（如 "AI 生成中"）
    stage_label   VARCHAR(50),
    -- 成功时存储生成结果（如图结构 JSON 或校验结果）
    result_json   JSONB,
    -- 失败时的错误信息
    error_message TEXT,
    created_by    VARCHAR(32)  NOT NULL,
    -- 关联的项目（可为空，如纯文档解析任务）
    project_id    VARCHAR(32),
    -- 使用的 AI 模型（如 "qwen-plus"）
    model         VARCHAR(50),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_ai_tasks         PRIMARY KEY (id),
    CONSTRAINT fk_ait_creator FOREIGN KEY (created_by)
        REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT fk_ait_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE
);

COMMENT ON TABLE  ai_tasks              IS 'AI 异步任务表';
COMMENT ON COLUMN ai_tasks.id           IS '任务ID，格式：task_{timestamp}_{随机}';
COMMENT ON COLUMN ai_tasks.type         IS '任务类型：generateFaultTree / generateKnowledgeGraph / parseDocument / validateGraph';
COMMENT ON COLUMN ai_tasks.status       IS '任务状态：pending / parsing / generating / completed / failed / cancelled';
COMMENT ON COLUMN ai_tasks.progress     IS '进度 [0,100]';
COMMENT ON COLUMN ai_tasks.result_json  IS '任务成功后的结果数据（JSONB）';
COMMENT ON COLUMN ai_tasks.model        IS '使用的 AI 模型名称';


-- ─── 2. AI 对话会话表 ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ai_conversations (
    id         VARCHAR(32) NOT NULL,
    project_id VARCHAR(32) NOT NULL,
    user_id    VARCHAR(32) NOT NULL,
    -- 会话类型（对应前端 type 字段）
    type       VARCHAR(20) NOT NULL DEFAULT 'faultTree'
                           CHECK (type IN ('faultTree', 'knowledgeGraph')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_ai_conversations       PRIMARY KEY (id),
    CONSTRAINT fk_aconv_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_aconv_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  ai_conversations        IS 'AI 对话会话表（二期，当前前端尚未接入）';
COMMENT ON COLUMN ai_conversations.id     IS '会话ID，格式：conv_{timestamp}_{随机}';
COMMENT ON COLUMN ai_conversations.type   IS '对话类型：faultTree / knowledgeGraph';


-- ─── 3. AI 对话消息表 ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ai_chat_messages (
    id              VARCHAR(32) NOT NULL,
    conversation_id VARCHAR(32) NOT NULL,
    -- 消息来源（用户 / AI / 系统）
    role            VARCHAR(20) NOT NULL
                                CHECK (role IN ('user', 'assistant', 'system')),
    content         TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_ai_chat_messages       PRIMARY KEY (id),
    CONSTRAINT fk_acm_conversation FOREIGN KEY (conversation_id)
        REFERENCES ai_conversations(id) ON DELETE CASCADE
);

COMMENT ON TABLE  ai_chat_messages              IS 'AI 对话消息表';
COMMENT ON COLUMN ai_chat_messages.id           IS '消息ID，格式：msg_{timestamp}_{随机}';
COMMENT ON COLUMN ai_chat_messages.role         IS '消息角色：user / assistant / system';
COMMENT ON COLUMN ai_chat_messages.content      IS '消息正文';
