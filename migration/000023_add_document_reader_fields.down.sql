-- ============================================================
-- Migration: 000023_add_document_reader_fields (down)
-- ============================================================

DROP INDEX IF EXISTS idx_documents_project_reader_kind;
DROP INDEX IF EXISTS idx_documents_project_preview_status;
DROP INDEX IF EXISTS idx_documents_project_uploaded_at;

ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS fk_documents_derived_pdf;

ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS documents_preview_status_check;

ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS documents_reader_kind_check;

ALTER TABLE documents
    DROP COLUMN IF EXISTS preview_error_message,
    DROP COLUMN IF EXISTS derived_pdf_doc_id,
    DROP COLUMN IF EXISTS preview_status,
    DROP COLUMN IF EXISTS reader_kind;
