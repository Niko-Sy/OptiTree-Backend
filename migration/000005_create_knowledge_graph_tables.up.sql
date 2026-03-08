-- ============================================================
-- Migration: 000005_create_knowledge_graph_tables
-- Description: 知识图谱实体节点表 + 知识图谱关系边表
--   - 节点/边使用 (id, project_id) 复合主键
--   - 样式数据存入 JSONB 避免字段膨胀，同时保留对 React Flow 的完整兼容
-- ============================================================

-- ─── 知识图谱节点表 ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS knowledge_graph_nodes (
    id           VARCHAR(32)  NOT NULL,
    project_id   VARCHAR(32)  NOT NULL,

    -- React Flow 节点类型
    type         VARCHAR(20)  NOT NULL
                              CHECK (type IN ('entityNode', 'eventNode', 'causeNode')),

    -- 节点在画布上的坐标（对应 React Flow position）
    position_x   DOUBLE PRECISION NOT NULL DEFAULT 0,
    position_y   DOUBLE PRECISION NOT NULL DEFAULT 0,

    -- 节点显示标签（data.label）
    label        VARCHAR(60)  NOT NULL,

    -- 实体语义类型（data.entityType）
    entity_type  VARCHAR(20)  NOT NULL
                              CHECK (entity_type IN ('component', 'event', 'cause', 'other')),

    -- 描述（data.description）
    description  VARCHAR(200),

    -- 来源文档名（data.sourceDoc）
    source_doc   VARCHAR(255),

    -- 节点样式（React Flow style + 自定义字段），完整 JSONB 存储
    style_json   JSONB        NOT NULL DEFAULT '{}',

    -- 节点 data 中其他扩展字段（除 label/entityType/description/sourceDoc 外）
    data_ext_json JSONB       NOT NULL DEFAULT '{}',

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
    id                VARCHAR(32) NOT NULL,
    project_id        VARCHAR(32) NOT NULL,
    source_node_id    VARCHAR(32) NOT NULL,
    target_node_id    VARCHAR(32) NOT NULL,

    -- 关系标签（最长30字）
    label             VARCHAR(30),

    -- React Flow 边类型（默认 smoothstep）
    type              VARCHAR(20) NOT NULL DEFAULT 'smoothstep',
    animated          BOOLEAN     NOT NULL DEFAULT FALSE,

    -- 样式字段（React Flow style / labelStyle / labelBgStyle）
    style_json        JSONB       NOT NULL DEFAULT '{}',
    label_style_json  JSONB       NOT NULL DEFAULT '{}',
    label_bg_style_json JSONB     NOT NULL DEFAULT '{}',

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
