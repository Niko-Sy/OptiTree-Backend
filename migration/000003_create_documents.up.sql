-- ============================================================
-- Migration: 000003_create_documents
-- Description: 文档上传记录表 —— 支持 AI 生成流程的文件解析状态追踪
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
