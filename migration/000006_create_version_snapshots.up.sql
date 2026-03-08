-- ============================================================
-- Migration: 000006_create_version_snapshots
-- Description: 版本快照表 —— 故障树/知识图谱全量快照，支持回滚
--   - 每个项目最多保留最近 30 个版本（由应用层控制）
--   - snapshot_json 兼容两种结构：
--       ft : { "nodes": [...], "edges": [...] }
--       kg : { "rfNodes": [...], "rfEdges": [...] }
-- ============================================================

CREATE TABLE IF NOT EXISTS version_snapshots (
    id           VARCHAR(32)  NOT NULL,
    project_id   VARCHAR(32)  NOT NULL,
    project_type VARCHAR(2)   NOT NULL
                              CHECK (project_type IN ('ft', 'kg')),

    -- 版本展示名（如 "版本 2026/3/7 16:20:30"）
    label        VARCHAR(100) NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_by   VARCHAR(32)  NOT NULL,

    -- 完整图数据快照，故障树或知识图谱结构均存此字段
    snapshot_json JSONB       NOT NULL,

    CONSTRAINT pk_version_snapshots       PRIMARY KEY (id),
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
