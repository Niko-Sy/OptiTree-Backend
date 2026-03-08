-- ============================================================
-- Migration: 000004_create_fault_tree_tables
-- Description: 故障树节点表 + 故障树边表
--   - 节点与边均使用 (id, project_id) 复合主键
--   - 图数据以「整体批量替换」模式保存，不做边→节点的外键约束
--   - revision 乐观锁在 projects 表维护
-- ============================================================

-- ─── 故障树节点表 ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS fault_tree_nodes (
    -- 前端生成的节点ID（如 "root"、"gate1"），在同一项目内唯一
    id                  VARCHAR(32)  NOT NULL,
    project_id          VARCHAR(32)  NOT NULL,

    -- 节点类型
    type                VARCHAR(20)  NOT NULL
                                     CHECK (type IN ('topEvent', 'midEvent', 'basicEvent', 'gate')),
    name                VARCHAR(60)  NOT NULL,

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
    priority            INT          NOT NULL DEFAULT 0,
    show_probability    BOOLEAN      NOT NULL DEFAULT FALSE,

    -- 校验规则，JSONB 数组（前端 rules 字段）
    rules               JSONB        NOT NULL DEFAULT '[]',

    investigate_method  TEXT,

    -- 关联文档ID数组
    documents           TEXT[]       NOT NULL DEFAULT '{}',

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
