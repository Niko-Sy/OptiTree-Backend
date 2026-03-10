-- ============================================================
-- OptiTree 数据库完整建表脚本
-- 技术栈：PostgreSQL
-- 生成时间：2026-03-08
-- 说明：本文件由 000001~000012 所有 .up.sql 按依赖顺序合并而成，
--       可一次性执行完成全库初始化，适用于开发/CI 环境快速搭建。
-- ============================================================


-- ============================================================
-- [1] 用户表 —— 系统所有认证与权限的起点
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

COMMENT ON TABLE  users               IS '用户主表';
COMMENT ON COLUMN users.id            IS '用户ID，业务生成，格式：user_{timestamp}_{随机}';
COMMENT ON COLUMN users.username      IS '用户名，3-20位，允许字母/数字/下划线/中文，全局唯一';
COMMENT ON COLUMN users.display_name  IS '显示名称，最长30字';
COMMENT ON COLUMN users.email         IS '邮箱，合法格式，全局唯一';
COMMENT ON COLUMN users.password_hash IS 'bcrypt哈希后的密码，原始密码不落库';
COMMENT ON COLUMN users.avatar        IS '头像URL（CDN地址）';
COMMENT ON COLUMN users.role          IS '系统角色：user / admin';
COMMENT ON COLUMN users.status        IS '账号状态：active / inactive / banned';
COMMENT ON COLUMN users.last_login_at IS '最近一次登录时间';


-- ============================================================
-- [2] 项目主表 —— 故障树(ft) 与 知识图谱(kg) 共用
-- ============================================================

CREATE TABLE IF NOT EXISTS projects (
    id                VARCHAR(32)  NOT NULL,
    name              VARCHAR(50)  NOT NULL,
    type              VARCHAR(2)   NOT NULL
                                   CHECK (type IN ('ft', 'kg')),
    description       VARCHAR(200),
    -- PostgreSQL 原生数组，存储标签
    tags              TEXT[]       NOT NULL DEFAULT '{}',

    -- 故障树统计（type='ft' 时使用）
    node_count        INT          NOT NULL DEFAULT 0,
    edge_count        INT          NOT NULL DEFAULT 0,
    -- 知识图谱统计（type='kg' 时使用）
    entity_count      INT          NOT NULL DEFAULT 0,
    relation_count    INT          NOT NULL DEFAULT 0,

    -- 乐观锁版本号：每次保存图数据后递增，用于并发控制
    graph_revision    INT          NOT NULL DEFAULT 0,

    -- 最新版本快照ID（无外键约束，避免与 version_snapshots 循环依赖）
    latest_version_id VARCHAR(32),
    member_count      INT          NOT NULL DEFAULT 0,
    -- AI 生成状态（仅 AI 生成流程使用）
    generation_status VARCHAR(32)
                                  CHECK (generation_status IN ('pending_generating', 'generating', 'completed', 'failed')),

    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_by        VARCHAR(32)  NOT NULL,

    CONSTRAINT pk_projects       PRIMARY KEY (id),
    CONSTRAINT fk_projects_owner FOREIGN KEY (created_by)
        REFERENCES users(id) ON DELETE RESTRICT
);

COMMENT ON TABLE  projects                   IS '项目主表，故障树(ft)和知识图谱(kg)共用';
COMMENT ON COLUMN projects.id                IS '项目ID，格式：proj_{timestamp}_{随机}';
COMMENT ON COLUMN projects.type              IS '项目类型：ft=故障树，kg=知识图谱';
COMMENT ON COLUMN projects.tags              IS '标签数组，使用 PostgreSQL Text Array';
COMMENT ON COLUMN projects.graph_revision    IS '图数据乐观锁版本号，保存时用于冲突检测';
COMMENT ON COLUMN projects.latest_version_id IS '最新版本快照ID，不加外键避免循环依赖';
COMMENT ON COLUMN projects.node_count        IS '故障树节点数（ft项目）';
COMMENT ON COLUMN projects.edge_count        IS '故障树边数（ft项目）';
COMMENT ON COLUMN projects.entity_count      IS '知识图谱实体数（kg项目）';
COMMENT ON COLUMN projects.relation_count    IS '知识图谱关系数（kg项目）';
COMMENT ON COLUMN projects.member_count      IS '协作成员数（含创建者）';
COMMENT ON COLUMN projects.generation_status IS 'AI 生成状态：pending_generating / generating / completed / failed';


-- ============================================================
-- [3] 文档上传记录表 —— 支持 AI 生成流程的文件解析状态追踪
-- ============================================================

CREATE TABLE IF NOT EXISTS documents (
    id               VARCHAR(32)  NOT NULL,
    file_name        VARCHAR(255) NOT NULL,
    file_type        VARCHAR(10)  NOT NULL
                                  CHECK (file_type IN ('pdf', 'doc', 'docx', 'xlsx', 'txt')),
    mime_type        VARCHAR(50)  NOT NULL,
    -- 文件大小，单位 Byte
    size             BIGINT       NOT NULL,
    status           VARCHAR(20)  NOT NULL DEFAULT 'pending'
                                  CHECK (status IN ('pending', 'parsing', 'parsed', 'failed')),
    -- AI 解析摘要（例："提取实体 56 个，关系 23 条"）
    summary          TEXT,
    -- 文件原始存储地址（CDN）
    source_url       TEXT         NOT NULL,
    -- 文本提取结果存储地址（供 AI 使用）
    text_extract_url TEXT,
    uploaded_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    uploaded_by      VARCHAR(32)  NOT NULL,
    -- 关联项目（可为空：用户上传后尚未绑定到具体项目）
    project_id       VARCHAR(32),

    CONSTRAINT pk_documents          PRIMARY KEY (id),
    CONSTRAINT fk_documents_uploader FOREIGN KEY (uploaded_by)
        REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT fk_documents_project  FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE SET NULL
);

COMMENT ON TABLE  documents                  IS '文档上传记录表';
COMMENT ON COLUMN documents.id               IS '文档ID，格式：doc_{timestamp}_{序号}';
COMMENT ON COLUMN documents.file_type        IS '文件类型：pdf / doc / docx / xlsx / txt';
COMMENT ON COLUMN documents.size             IS '文件大小，单位 Byte';
COMMENT ON COLUMN documents.status           IS '解析状态：pending / parsing / parsed / failed';
COMMENT ON COLUMN documents.summary          IS 'AI解析摘要文本';
COMMENT ON COLUMN documents.source_url       IS '文件CDN存储地址';
COMMENT ON COLUMN documents.text_extract_url IS 'AI文本提取结果地址（.txt）';
COMMENT ON COLUMN documents.project_id       IS '关联项目，项目删除后置空';


-- ============================================================
-- [4] 故障树节点表 + 故障树边表
--   - 节点与边均使用 (id, project_id) 复合主键
--   - 图数据以「整体批量替换」模式保存，不做边→节点的外键约束
--   - revision 乐观锁在 projects 表维护
-- ============================================================

-- ─── 故障树节点表 ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS fault_tree_nodes (
    -- 前端生成的节点ID（如 "root"、"gate1"），在同一项目内唯一
    id                  VARCHAR(32)      NOT NULL,
    project_id          VARCHAR(32)      NOT NULL,

    -- 节点类型
    type                VARCHAR(20)      NOT NULL
                                         CHECK (type IN ('topEvent', 'midEvent', 'basicEvent', 'gate')),
    name                VARCHAR(60)      NOT NULL,

    -- 画布坐标与尺寸
    x                   DOUBLE PRECISION NOT NULL DEFAULT 0,
    y                   DOUBLE PRECISION NOT NULL DEFAULT 0,
    width               DOUBLE PRECISION NOT NULL DEFAULT 140,
    height              DOUBLE PRECISION NOT NULL DEFAULT 60,

    -- 仅 basicEvent 使用，范围 [0, 1]
    probability         DOUBLE PRECISION
                                         CHECK (probability IS NULL
                                                OR (probability >= 0 AND probability <= 1)),

    -- 仅 gate 节点使用
    gate_type           VARCHAR(10)
                                         CHECK (gate_type IS NULL
                                                OR gate_type IN ('AND', 'OR', 'NOT')),

    event_id            VARCHAR(20),
    description         TEXT,
    error_level         VARCHAR(10)
                                         CHECK (error_level IS NULL
                                                OR error_level IN ('P1', 'P2', 'P3')),
    priority            INT              NOT NULL DEFAULT 0,
    show_probability    BOOLEAN          NOT NULL DEFAULT FALSE,

    -- 校验规则，JSONB 数组（前端 rules 字段）
    rules               JSONB            NOT NULL DEFAULT '[]',

    investigate_method  TEXT,

    -- 关联文档ID数组
    documents           TEXT[]           NOT NULL DEFAULT '{}',

    -- 转移门目标（transfer gate）
    transfer            TEXT,

    CONSTRAINT pk_fault_tree_nodes PRIMARY KEY (id, project_id),
    CONSTRAINT fk_ftn_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE
);

COMMENT ON TABLE  fault_tree_nodes                   IS '故障树节点表（当前实时图数据，非版本快照）';
COMMENT ON COLUMN fault_tree_nodes.id                IS '节点ID，前端生成，在同一项目内唯一';
COMMENT ON COLUMN fault_tree_nodes.type              IS '节点类型：topEvent/midEvent/basicEvent/gate';
COMMENT ON COLUMN fault_tree_nodes.probability       IS '基本事件发生概率 [0,1]，仅 basicEvent 使用';
COMMENT ON COLUMN fault_tree_nodes.gate_type         IS '逻辑门类型：AND/OR/NOT，仅 gate 节点使用';
COMMENT ON COLUMN fault_tree_nodes.error_level       IS '故障等级：P1/P2/P3';
COMMENT ON COLUMN fault_tree_nodes.rules             IS '校验规则列表，JSONB 数组';
COMMENT ON COLUMN fault_tree_nodes.documents         IS '绑定文档ID数组';
COMMENT ON COLUMN fault_tree_nodes.transfer          IS '转移门指向的目标节点ID';


-- ─── 故障树边表 ───────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS fault_tree_edges (
    -- 前端生成的边ID（如 "e1"），在同一项目内唯一
    id           VARCHAR(32) NOT NULL,
    project_id   VARCHAR(32) NOT NULL,
    from_node_id VARCHAR(32) NOT NULL,
    to_node_id   VARCHAR(32) NOT NULL,

    CONSTRAINT pk_fault_tree_edges PRIMARY KEY (id, project_id),
    CONSTRAINT fk_fte_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE
);

COMMENT ON TABLE  fault_tree_edges              IS '故障树边表（当前实时图数据，非版本快照）';
COMMENT ON COLUMN fault_tree_edges.from_node_id IS '父节点ID（方向：父 → 子）';
COMMENT ON COLUMN fault_tree_edges.to_node_id   IS '子节点ID';


-- ============================================================
-- [5] 知识图谱实体节点表 + 知识图谱关系边表
--   - 节点/边使用 (id, project_id) 复合主键
--   - 样式数据存入 JSONB 避免字段膨胀，同时保留对 React Flow 的完整兼容
-- ============================================================

-- ─── 知识图谱节点表 ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS knowledge_graph_nodes (
    id            VARCHAR(32)      NOT NULL,
    project_id    VARCHAR(32)      NOT NULL,

    -- React Flow 节点类型
    type          VARCHAR(20)      NOT NULL
                                   CHECK (type IN ('entityNode', 'eventNode', 'causeNode')),

    -- 节点在画布上的坐标（对应 React Flow position）
    position_x    DOUBLE PRECISION NOT NULL DEFAULT 0,
    position_y    DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- 节点显示标签（data.label）
    label         VARCHAR(60)      NOT NULL,

    -- 实体语义类型（data.entityType）
    entity_type   VARCHAR(20)      NOT NULL
                                   CHECK (entity_type IN ('component', 'event', 'cause', 'other')),

    -- 描述（data.description）
    description   VARCHAR(200),

    -- 来源文档名（data.sourceDoc）
    source_doc    VARCHAR(255),

    -- 节点样式（React Flow style + 自定义字段），完整 JSONB 存储
    style_json    JSONB            NOT NULL DEFAULT '{}',

    -- 节点 data 中其他扩展字段（除 label/entityType/description/sourceDoc 外）
    data_ext_json JSONB            NOT NULL DEFAULT '{}',

    CONSTRAINT pk_knowledge_graph_nodes PRIMARY KEY (id, project_id),
    CONSTRAINT fk_kgn_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE
);

COMMENT ON TABLE  knowledge_graph_nodes               IS '知识图谱节点表（当前实时图数据，非版本快照）';
COMMENT ON COLUMN knowledge_graph_nodes.id            IS '节点ID，前端生成，在同一项目内唯一';
COMMENT ON COLUMN knowledge_graph_nodes.type          IS 'React Flow 节点类型：entityNode/eventNode/causeNode';
COMMENT ON COLUMN knowledge_graph_nodes.label         IS '节点显示名称，最长60字';
COMMENT ON COLUMN knowledge_graph_nodes.entity_type   IS '实体语义类型：component/event/cause/other';
COMMENT ON COLUMN knowledge_graph_nodes.style_json    IS 'React Flow style 对象（width/height/backgroundColor 等）';
COMMENT ON COLUMN knowledge_graph_nodes.data_ext_json IS 'data 字段其余扩展内容';


-- ─── 知识图谱边表 ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS knowledge_graph_edges (
    id                  VARCHAR(32) NOT NULL,
    project_id          VARCHAR(32) NOT NULL,
    source_node_id      VARCHAR(32) NOT NULL,
    target_node_id      VARCHAR(32) NOT NULL,

    -- 关系标签（最长30字）
    label               VARCHAR(30),

    -- React Flow 边类型（默认 smoothstep）
    type                VARCHAR(20) NOT NULL DEFAULT 'smoothstep',
    animated            BOOLEAN     NOT NULL DEFAULT FALSE,

    -- 样式字段（React Flow style / labelStyle / labelBgStyle）
    style_json          JSONB       NOT NULL DEFAULT '{}',
    label_style_json    JSONB       NOT NULL DEFAULT '{}',
    label_bg_style_json JSONB       NOT NULL DEFAULT '{}',

    CONSTRAINT pk_knowledge_graph_edges PRIMARY KEY (id, project_id),
    CONSTRAINT fk_kge_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE
);

COMMENT ON TABLE  knowledge_graph_edges                  IS '知识图谱边表（当前实时图数据，非版本快照）';
COMMENT ON COLUMN knowledge_graph_edges.label            IS '关系标签，最长30字';
COMMENT ON COLUMN knowledge_graph_edges.type             IS 'React Flow 边类型，默认 smoothstep';
COMMENT ON COLUMN knowledge_graph_edges.style_json       IS '边线条样式（stroke/strokeWidth 等）';
COMMENT ON COLUMN knowledge_graph_edges.label_style_json IS '标签文字样式（fontSize/fill 等）';
COMMENT ON COLUMN knowledge_graph_edges.label_bg_style_json IS '标签背景样式（fill/fillOpacity 等）';


-- ============================================================
-- [6] 版本快照表 —— 故障树/知识图谱全量快照，支持回滚
--   - 每个项目最多保留最近 30 个版本（由应用层控制）
--   - snapshot_json 兼容两种结构：
--       ft : { "nodes": [...], "edges": [...] }
--       kg : { "rfNodes": [...], "rfEdges": [...] }
-- ============================================================

CREATE TABLE IF NOT EXISTS version_snapshots (
    id            VARCHAR(32)  NOT NULL,
    project_id    VARCHAR(32)  NOT NULL,
    project_type  VARCHAR(2)   NOT NULL
                               CHECK (project_type IN ('ft', 'kg')),

    -- 版本展示名（如 "版本 2026/3/7 16:20:30"）
    label         VARCHAR(100) NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_by    VARCHAR(32)  NOT NULL,

    -- 完整图数据快照，故障树或知识图谱结构均存此字段
    snapshot_json JSONB        NOT NULL,

    CONSTRAINT pk_version_snapshots PRIMARY KEY (id),
    CONSTRAINT fk_vs_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_vs_creator FOREIGN KEY (created_by)
        REFERENCES users(id) ON DELETE RESTRICT
);

COMMENT ON TABLE  version_snapshots               IS '版本快照表，每项目最多保留近30条';
COMMENT ON COLUMN version_snapshots.id            IS '版本ID，格式：ver_{timestamp}_{随机}';
COMMENT ON COLUMN version_snapshots.project_type  IS '冗余项目类型：ft / kg，方便查询时免 JOIN';
COMMENT ON COLUMN version_snapshots.label         IS '版本显示名称';
COMMENT ON COLUMN version_snapshots.snapshot_json IS '全量图数据快照（JSONB）';


-- ============================================================
-- [7] 项目成员表 —— 协作权限管理（admin / editor / viewer）
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

COMMENT ON TABLE  project_members           IS '项目协作成员表';
COMMENT ON COLUMN project_members.id        IS '成员记录ID，格式：member_{timestamp}_{随机}';
COMMENT ON COLUMN project_members.role      IS '项目内角色：admin(管理员) / editor(编辑者) / viewer(查看者)';
COMMENT ON COLUMN project_members.status    IS '成员状态：active / inactive';
COMMENT ON COLUMN project_members.joined_at IS '加入时间';


-- ============================================================
-- [8] 项目邀请表 —— 邮件邀请流程（含接受/拒绝/过期状态）
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

    CONSTRAINT pk_invitations       PRIMARY KEY (id),
    -- token 全局唯一，防止碰撞
    CONSTRAINT uq_invitations_token UNIQUE (token),
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


-- ============================================================
-- [9] AI 相关三表
--   1. ai_tasks          —— 异步任务（文档解析 / AI 生成 / 校验）
--   2. ai_conversations  —— AI 对话会话（按项目归属）
--   3. ai_chat_messages  —— 对话消息记录
-- ============================================================

-- ─── AI 异步任务表 ────────────────────────────────────────────
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

    CONSTRAINT pk_ai_tasks    PRIMARY KEY (id),
    CONSTRAINT fk_ait_creator FOREIGN KEY (created_by)
        REFERENCES users(id) ON DELETE RESTRICT,
    CONSTRAINT fk_ait_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE
);

COMMENT ON TABLE  ai_tasks             IS 'AI 异步任务表';
COMMENT ON COLUMN ai_tasks.id          IS '任务ID，格式：task_{timestamp}_{随机}';
COMMENT ON COLUMN ai_tasks.type        IS '任务类型：generateFaultTree / generateKnowledgeGraph / parseDocument / validateGraph';
COMMENT ON COLUMN ai_tasks.status      IS '任务状态：pending / parsing / generating / completed / failed / cancelled';
COMMENT ON COLUMN ai_tasks.progress    IS '进度 [0,100]';
COMMENT ON COLUMN ai_tasks.result_json IS '任务成功后的结果数据（JSONB）';
COMMENT ON COLUMN ai_tasks.model       IS '使用的 AI 模型名称';


-- ─── AI 对话会话表 ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ai_conversations (
    id         VARCHAR(32) NOT NULL,
    project_id VARCHAR(32) NOT NULL,
    user_id    VARCHAR(32) NOT NULL,
    -- 会话类型（对应前端 type 字段）
    type       VARCHAR(20) NOT NULL DEFAULT 'faultTree'
                           CHECK (type IN ('faultTree', 'knowledgeGraph')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_ai_conversations  PRIMARY KEY (id),
    CONSTRAINT fk_aconv_project FOREIGN KEY (project_id)
        REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_aconv_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  ai_conversations      IS 'AI 对话会话表（二期，当前前端尚未接入）';
COMMENT ON COLUMN ai_conversations.id   IS '会话ID，格式：conv_{timestamp}_{随机}';
COMMENT ON COLUMN ai_conversations.type IS '对话类型：faultTree / knowledgeGraph';


-- ─── AI 对话消息表 ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ai_chat_messages (
    id              VARCHAR(32) NOT NULL,
    conversation_id VARCHAR(32) NOT NULL,
    -- 消息来源（用户 / AI / 系统）
    role            VARCHAR(20) NOT NULL
                                CHECK (role IN ('user', 'assistant', 'system')),
    content         TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pk_ai_chat_messages  PRIMARY KEY (id),
    CONSTRAINT fk_acm_conversation FOREIGN KEY (conversation_id)
        REFERENCES ai_conversations(id) ON DELETE CASCADE
);

COMMENT ON TABLE  ai_chat_messages         IS 'AI 对话消息表';
COMMENT ON COLUMN ai_chat_messages.id      IS '消息ID，格式：msg_{timestamp}_{随机}';
COMMENT ON COLUMN ai_chat_messages.role    IS '消息角色：user / assistant / system';
COMMENT ON COLUMN ai_chat_messages.content IS '消息正文';


-- ============================================================
-- [10] 认证支撑三表
--   1. refresh_tokens       —— Refresh Token 持久化（Redis 主存，DB 备份）
--   2. login_logs           —— 登录记录（用于用户中心展示）
--   3. user_social_bindings —— OAuth 社交账号绑定（GitHub / 微信 / QQ）
-- ============================================================

-- ─── Refresh Token 表 ────────────────────────────────────────
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

    CONSTRAINT pk_refresh_tokens      PRIMARY KEY (id),
    CONSTRAINT uq_refresh_tokens_hash UNIQUE (token_hash),
    CONSTRAINT fk_rt_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  refresh_tokens            IS 'Refresh Token 持久化表';
COMMENT ON COLUMN refresh_tokens.token_hash IS 'Token的bcrypt哈希，原始值不落库';
COMMENT ON COLUMN refresh_tokens.is_revoked IS '是否已撤销（登出/踢出设备后为 true）';


-- ─── 登录记录表 ───────────────────────────────────────────────
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


-- ─── 社交账号绑定表 ───────────────────────────────────────────
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
    CONSTRAINT uq_usb_provider_uid  UNIQUE (provider, provider_user_id),
    -- 同一用户在同一平台只能绑定一次
    CONSTRAINT uq_usb_user_provider UNIQUE (user_id, provider),
    CONSTRAINT fk_usb_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  user_social_bindings                  IS 'OAuth 社交账号绑定表';
COMMENT ON COLUMN user_social_bindings.provider         IS '平台：github / wechat / qq';
COMMENT ON COLUMN user_social_bindings.provider_user_id IS '平台侧用户唯一标识';
COMMENT ON COLUMN user_social_bindings.access_token     IS '平台 Access Token（加密存储）';


-- ============================================================
-- [11] 通知与审计日志两表
--   1. notifications —— 站内通知（邀请/版本/AI完成/系统公告）
--   2. audit_logs    —— 操作审计日志（用于"操作日志"入口）
-- ============================================================

-- ─── 通知表 ───────────────────────────────────────────────────
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

    CONSTRAINT pk_notifications  PRIMARY KEY (id),
    CONSTRAINT fk_notif_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE CASCADE
);

COMMENT ON TABLE  notifications             IS '站内通知表';
COMMENT ON COLUMN notifications.id          IS '通知ID，格式：notif_{timestamp}_{随机}';
COMMENT ON COLUMN notifications.type        IS '通知类型';
COMMENT ON COLUMN notifications.is_read     IS '是否已读';
COMMENT ON COLUMN notifications.resource_id IS '关联资源ID（邀请/版本/任务等）';
COMMENT ON COLUMN notifications.extra_json  IS '扩展数据（如项目名称、版本标签等）';


-- ─── 操作审计日志表 ───────────────────────────────────────────
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

    CONSTRAINT pk_audit_logs PRIMARY KEY (id),
    CONSTRAINT fk_al_user FOREIGN KEY (user_id)
        REFERENCES users(id) ON DELETE SET NULL
);

COMMENT ON TABLE  audit_logs               IS '操作审计日志表，用于"操作日志"入口展示';
COMMENT ON COLUMN audit_logs.id            IS '日志ID，格式：log_{timestamp}_{随机}';
COMMENT ON COLUMN audit_logs.action        IS '操作类型，格式：{资源}.{动作}，如 project.delete';
COMMENT ON COLUMN audit_logs.resource_type IS '资源类型：project / version / member / graph 等';
COMMENT ON COLUMN audit_logs.operator_name IS '操作人展示名（冗余，防止用户删除后信息丢失）';
COMMENT ON COLUMN audit_logs.summary       IS '操作摘要（人类可读描述）';


-- ============================================================
-- [12] 全局索引
--   - 主键索引已由各表 PRIMARY KEY 自动创建，此处不重复
--   - 唯一索引亦已在建表时通过 UNIQUE 约束创建，此处不重复
--   - 本节仅创建「非唯一」查询加速索引
-- ============================================================

-- ─── users 表 ────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_users_status
    ON users (status);

CREATE INDEX IF NOT EXISTS idx_users_created_at
    ON users (created_at DESC);


-- ─── projects 表 ─────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_projects_created_by_updated_at
    ON projects (created_by, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_projects_type
    ON projects (type);

-- 全文搜索：项目名称（支持 keyword 搜索）
CREATE INDEX IF NOT EXISTS idx_projects_name
    ON projects USING gin (to_tsvector('simple', name));


-- ─── documents 表 ────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_documents_uploaded_by
    ON documents (uploaded_by);

CREATE INDEX IF NOT EXISTS idx_documents_project_id
    ON documents (project_id);

CREATE INDEX IF NOT EXISTS idx_documents_status
    ON documents (status);


-- ─── fault_tree_nodes 表 ─────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_ftn_project_id
    ON fault_tree_nodes (project_id);


-- ─── fault_tree_edges 表 ─────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_fte_project_id
    ON fault_tree_edges (project_id);


-- ─── knowledge_graph_nodes 表 ────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_kgn_project_id
    ON knowledge_graph_nodes (project_id);

CREATE INDEX IF NOT EXISTS idx_kgn_entity_type
    ON knowledge_graph_nodes (project_id, entity_type);


-- ─── knowledge_graph_edges 表 ────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_kge_project_id
    ON knowledge_graph_edges (project_id);


-- ─── version_snapshots 表 ────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_vs_project_created_at
    ON version_snapshots (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_vs_created_by
    ON version_snapshots (created_by, created_at DESC);


-- ─── project_members 表 ──────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_pm_user_id
    ON project_members (user_id);

CREATE INDEX IF NOT EXISTS idx_pm_project_id
    ON project_members (project_id);


-- ─── invitations 表 ──────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_inv_email_status
    ON invitations (email, status);

CREATE INDEX IF NOT EXISTS idx_inv_project_status
    ON invitations (project_id, status);


-- ─── ai_tasks 表 ─────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_ait_created_by_status
    ON ai_tasks (created_by, status);

CREATE INDEX IF NOT EXISTS idx_ait_project_id
    ON ai_tasks (project_id);

CREATE INDEX IF NOT EXISTS idx_ait_status_created_at
    ON ai_tasks (status, created_at);


-- ─── ai_conversations 表 ─────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_aconv_project_user
    ON ai_conversations (project_id, user_id);


-- ─── ai_chat_messages 表 ─────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_acm_conversation_created_at
    ON ai_chat_messages (conversation_id, created_at);


-- ─── refresh_tokens 表 ───────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_rt_user_id
    ON refresh_tokens (user_id);

CREATE INDEX IF NOT EXISTS idx_rt_expires_revoked
    ON refresh_tokens (expires_at, is_revoked);


-- ─── login_logs 表 ───────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_ll_user_created_at
    ON login_logs (user_id, created_at DESC);


-- ─── notifications 表 ────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_notif_user_read
    ON notifications (user_id, is_read, created_at DESC);


-- ─── audit_logs 表 ───────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_al_user_id
    ON audit_logs (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_al_project_id
    ON audit_logs (project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_al_action
    ON audit_logs (action);
