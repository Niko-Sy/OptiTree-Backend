-- ============================================================
-- Migration: 000023_add_document_reader_fields
-- Description: 为文档阅读器补齐预览与定位相关元数据
-- ============================================================

ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS reader_kind VARCHAR(20),
    ADD COLUMN IF NOT EXISTS preview_status VARCHAR(20) DEFAULT 'ready',
    ADD COLUMN IF NOT EXISTS derived_pdf_doc_id VARCHAR(32),
    ADD COLUMN IF NOT EXISTS preview_error_message TEXT;

UPDATE documents
SET reader_kind = CASE
    WHEN file_type = 'pdf' THEN 'pdf'
    WHEN file_type = 'docx' THEN 'pdf'
    WHEN file_type IN ('xlsx') THEN 'tabular'
    WHEN file_type IN ('txt') THEN 'text'
    ELSE 'unsupported'
END
WHERE reader_kind IS NULL OR reader_kind = '';

UPDATE documents
SET preview_status = CASE
    WHEN file_type = 'docx' THEN 'processing'
    WHEN COALESCE(reader_kind, '') = 'unsupported' THEN 'failed'
    ELSE 'ready'
END
WHERE preview_status IS NULL OR preview_status = '';

ALTER TABLE documents
    ALTER COLUMN preview_status SET NOT NULL;

ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS documents_reader_kind_check;
ALTER TABLE documents
    ADD CONSTRAINT documents_reader_kind_check
    CHECK (reader_kind IN ('pdf', 'tabular', 'text', 'unsupported'));

ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS documents_preview_status_check;
ALTER TABLE documents
    ADD CONSTRAINT documents_preview_status_check
    CHECK (preview_status IN ('ready', 'processing', 'failed'));

ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS fk_documents_derived_pdf;
ALTER TABLE documents
    ADD CONSTRAINT fk_documents_derived_pdf
    FOREIGN KEY (derived_pdf_doc_id)
    REFERENCES documents(id)
    ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_documents_project_uploaded_at
    ON documents(project_id, uploaded_at DESC);

CREATE INDEX IF NOT EXISTS idx_documents_project_preview_status
    ON documents(project_id, preview_status);

CREATE INDEX IF NOT EXISTS idx_documents_project_reader_kind
    ON documents(project_id, reader_kind);

COMMENT ON COLUMN documents.reader_kind IS '阅读器类型：pdf / tabular / text / unsupported';
COMMENT ON COLUMN documents.preview_status IS '预览状态：ready / processing / failed';
COMMENT ON COLUMN documents.derived_pdf_doc_id IS 'DOCX 转 PDF 后的衍生文档 ID';
COMMENT ON COLUMN documents.preview_error_message IS '预览失败原因';
