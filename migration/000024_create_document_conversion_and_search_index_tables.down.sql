-- ============================================================
-- Migration: 000024_create_document_conversion_and_search_index_tables (down)
-- ============================================================

DROP INDEX IF EXISTS idx_document_search_indexes_project_updated;
DROP INDEX IF EXISTS idx_document_search_indexes_document;
DROP INDEX IF EXISTS idx_document_search_indexes_project_document;
DROP TABLE IF EXISTS document_search_indexes;

DROP INDEX IF EXISTS idx_document_conversion_tasks_status_created_at;
DROP INDEX IF EXISTS uq_document_conversion_tasks_document_id;
DROP TABLE IF EXISTS document_conversion_tasks;
