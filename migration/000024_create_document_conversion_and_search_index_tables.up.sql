-- ============================================================
-- Migration: 000024_create_document_conversion_and_search_index_tables
-- Description: DOCX conversion tasks and offline document search indexes
-- ============================================================

CREATE TABLE IF NOT EXISTS document_conversion_tasks (
    id VARCHAR(32) PRIMARY KEY,
    document_id VARCHAR(32) NOT NULL,
    project_id VARCHAR(32),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 1,
    error_message TEXT,
    derived_pdf_doc_id VARCHAR(32),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_document_conversion_tasks_document
        FOREIGN KEY (document_id)
        REFERENCES documents(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_document_conversion_tasks_project
        FOREIGN KEY (project_id)
        REFERENCES projects(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_document_conversion_tasks_derived_pdf
        FOREIGN KEY (derived_pdf_doc_id)
        REFERENCES documents(id)
        ON DELETE SET NULL,

    CONSTRAINT document_conversion_tasks_status_check
        CHECK (status IN ('pending', 'processing', 'completed', 'failed'))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_document_conversion_tasks_document_id
    ON document_conversion_tasks(document_id);

CREATE INDEX IF NOT EXISTS idx_document_conversion_tasks_status_created_at
    ON document_conversion_tasks(status, created_at ASC);

CREATE TABLE IF NOT EXISTS document_search_indexes (
    id VARCHAR(32) PRIMARY KEY,
    document_id VARCHAR(32) NOT NULL,
    project_id VARCHAR(32) NOT NULL,
    reader_kind VARCHAR(20) NOT NULL,
    snippet TEXT NOT NULL,
    searchable_text TEXT NOT NULL,
    locator_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT fk_document_search_indexes_document
        FOREIGN KEY (document_id)
        REFERENCES documents(id)
        ON DELETE CASCADE,

    CONSTRAINT fk_document_search_indexes_project
        FOREIGN KEY (project_id)
        REFERENCES projects(id)
        ON DELETE CASCADE,

    CONSTRAINT document_search_indexes_reader_kind_check
        CHECK (reader_kind IN ('pdf', 'tabular', 'text'))
);

CREATE INDEX IF NOT EXISTS idx_document_search_indexes_project_document
    ON document_search_indexes(project_id, document_id);

CREATE INDEX IF NOT EXISTS idx_document_search_indexes_document
    ON document_search_indexes(document_id);

CREATE INDEX IF NOT EXISTS idx_document_search_indexes_project_updated
    ON document_search_indexes(project_id, updated_at DESC);

COMMENT ON TABLE document_conversion_tasks IS 'DOCX conversion placeholder async queue';
COMMENT ON COLUMN document_conversion_tasks.status IS 'pending / processing / completed / failed';
COMMENT ON COLUMN document_conversion_tasks.derived_pdf_doc_id IS 'Derived PDF document id for DOCX preview';

COMMENT ON TABLE document_search_indexes IS 'Offline extracted search blocks for project documents';
COMMENT ON COLUMN document_search_indexes.locator_json IS 'Serialized DocumentSearchLocator JSON';
