-- ============================================================
-- Migration: 000002_create_projects
-- Description: 项目主表 —— 故障树(ft) 与 知识图谱(kg) 共用
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

    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_by        VARCHAR(32)  NOT NULL,

    CONSTRAINT pk_projects       PRIMARY KEY (id),
    CONSTRAINT fk_projects_owner FOREIGN KEY (created_by)
        REFERENCES users(id) ON DELETE RESTRICT
);

COMMENT ON TABLE  projects                  IS '项目主表，故障树(ft)和知识图谱(kg)共用';
COMMENT ON COLUMN projects.id               IS '项目ID，格式：proj_{timestamp}_{随机}';
COMMENT ON COLUMN projects.type             IS '项目类型：ft=故障树，kg=知识图谱';
COMMENT ON COLUMN projects.tags             IS '标签数组，使用 PostgreSQL Text Array';
COMMENT ON COLUMN projects.graph_revision   IS '图数据乐观锁版本号，保存时用于冲突检测';
COMMENT ON COLUMN projects.latest_version_id IS '最新版本快照ID，不加外键避免循环依赖';
COMMENT ON COLUMN projects.node_count       IS '故障树节点数（ft项目）';
COMMENT ON COLUMN projects.edge_count       IS '故障树边数（ft项目）';
COMMENT ON COLUMN projects.entity_count     IS '知识图谱实体数（kg项目）';
COMMENT ON COLUMN projects.relation_count   IS '知识图谱关系数（kg项目）';
COMMENT ON COLUMN projects.member_count     IS '协作成员数（含创建者）';
