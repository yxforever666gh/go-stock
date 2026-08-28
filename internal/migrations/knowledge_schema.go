package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const knowledgeDocumentsTableSQL = `CREATE TABLE IF NOT EXISTS knowledge_documents (
  document_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(document_id)) > 0),
  title TEXT NOT NULL CHECK (length(trim(title)) > 0),
  description TEXT,
  tags_json TEXT NOT NULL DEFAULT '[]',
  document_type TEXT NOT NULL CHECK (document_type IN ('text', 'markdown', 'pdf', 'research_report', 'memory')),
  origin_type TEXT NOT NULL CHECK (origin_type IN ('upload', 'research_report', 'memory_candidate')),
  source_owner_type TEXT CHECK (source_owner_type IS NULL OR source_owner_type IN ('research1', 'research2')),
  source_owner_id TEXT CHECK (source_owner_id IS NULL OR length(trim(source_owner_id)) > 0),
  created_by_user_id TEXT NOT NULL CHECK (length(trim(created_by_user_id)) > 0),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  CHECK ((source_owner_type IS NULL AND source_owner_id IS NULL) OR
         (source_owner_type IS NOT NULL AND source_owner_id IS NOT NULL))
)`

const knowledgeDocumentVersionsTableSQL = `CREATE TABLE IF NOT EXISTS knowledge_document_versions (
  version_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(version_id)) > 0),
  document_id TEXT NOT NULL CHECK (length(trim(document_id)) > 0),
  version_no INTEGER NOT NULL CHECK (version_no >= 1),
  content_text TEXT NOT NULL,
  content_sha256 CHAR(64) NOT NULL CHECK (length(content_sha256) = 64 AND content_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  mime_type TEXT NOT NULL CHECK (length(trim(mime_type)) > 0),
  source_filename TEXT NOT NULL CHECK (length(trim(source_filename)) > 0),
  extraction_status TEXT NOT NULL CHECK (extraction_status IN ('pending', 'complete', 'failed')),
  extraction_error TEXT,
  created_by_user_id TEXT NOT NULL CHECK (length(trim(created_by_user_id)) > 0),
  created_at DATETIME NOT NULL,
  UNIQUE (document_id, version_no)
)`

const knowledgeVersionStatesTableSQL = `CREATE TABLE IF NOT EXISTS knowledge_version_states (
  state_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(state_id)) > 0),
  version_id TEXT NOT NULL UNIQUE CHECK (length(trim(version_id)) > 0),
  status TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'rejected', 'superseded')),
  approved_by_actor_type TEXT CHECK (approved_by_actor_type IS NULL OR approved_by_actor_type = 'user'),
  approved_by_user_id TEXT,
  approval_reason TEXT,
  approved_at DATETIME,
  rejected_by_actor_type TEXT CHECK (rejected_by_actor_type IS NULL OR rejected_by_actor_type = 'user'),
  rejected_by_user_id TEXT,
  rejection_reason TEXT,
  rejected_at DATETIME,
  superseded_by_actor_type TEXT CHECK (superseded_by_actor_type IS NULL OR superseded_by_actor_type = 'user'),
  superseded_by_user_id TEXT,
  superseded_reason TEXT,
  superseded_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  CHECK (
    (status = 'draft' AND approved_by_actor_type IS NULL AND approved_by_user_id IS NULL AND approval_reason IS NULL AND approved_at IS NULL AND rejected_by_actor_type IS NULL AND rejected_by_user_id IS NULL AND rejection_reason IS NULL AND rejected_at IS NULL AND superseded_by_actor_type IS NULL AND superseded_by_user_id IS NULL AND superseded_reason IS NULL AND superseded_at IS NULL) OR
    (status = 'approved' AND approved_by_actor_type = 'user' AND length(trim(approved_by_user_id)) > 0 AND approved_at IS NOT NULL AND rejected_by_actor_type IS NULL AND rejected_by_user_id IS NULL AND rejection_reason IS NULL AND rejected_at IS NULL AND superseded_by_actor_type IS NULL AND superseded_by_user_id IS NULL AND superseded_reason IS NULL AND superseded_at IS NULL) OR
    (status = 'rejected' AND approved_by_actor_type IS NULL AND approved_by_user_id IS NULL AND approval_reason IS NULL AND approved_at IS NULL AND rejected_by_actor_type = 'user' AND length(trim(rejected_by_user_id)) > 0 AND rejected_at IS NOT NULL AND superseded_by_actor_type IS NULL AND superseded_by_user_id IS NULL AND superseded_reason IS NULL AND superseded_at IS NULL) OR
    (status = 'superseded' AND approved_by_actor_type = 'user' AND length(trim(approved_by_user_id)) > 0 AND approved_at IS NOT NULL AND rejected_by_actor_type IS NULL AND rejected_by_user_id IS NULL AND rejection_reason IS NULL AND rejected_at IS NULL AND superseded_by_actor_type = 'user' AND length(trim(superseded_by_user_id)) > 0 AND superseded_at IS NOT NULL)
  )
)`

const knowledgeMemoryCandidatesTableSQL = `CREATE TABLE IF NOT EXISTS knowledge_memory_candidates (
  candidate_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(candidate_id)) > 0),
  source_owner_type TEXT NOT NULL CHECK (source_owner_type IN ('research1', 'research2')),
  source_owner_id TEXT NOT NULL CHECK (length(trim(source_owner_id)) > 0),
  title TEXT NOT NULL CHECK (length(trim(title)) > 0),
  content_text TEXT NOT NULL CHECK (length(trim(content_text)) > 0),
  content_sha256 CHAR(64) NOT NULL CHECK (length(content_sha256) = 64 AND content_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  status TEXT NOT NULL CHECK (status IN ('draft', 'approved', 'rejected')),
  proposed_by_actor_type TEXT NOT NULL CHECK (proposed_by_actor_type IN ('user', 'ai', 'system')),
  proposed_by_actor_id TEXT CHECK (proposed_by_actor_id IS NULL OR length(trim(proposed_by_actor_id)) > 0),
  decision_actor_type TEXT CHECK (decision_actor_type IS NULL OR decision_actor_type = 'user'),
  decision_by_user_id TEXT,
  decision_reason TEXT,
  approved_version_id TEXT,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  CHECK (
    (status = 'draft' AND decision_actor_type IS NULL AND decision_by_user_id IS NULL AND decision_reason IS NULL AND approved_version_id IS NULL) OR
    (status = 'approved' AND decision_actor_type = 'user' AND length(trim(decision_by_user_id)) > 0) OR
    (status = 'rejected' AND decision_actor_type = 'user' AND length(trim(decision_by_user_id)) > 0 AND approved_version_id IS NULL)
  )
)`

const knowledgeRetrievalRunsTableSQL = `CREATE TABLE IF NOT EXISTS knowledge_retrieval_runs (
  retrieval_run_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(retrieval_run_id)) > 0),
  owner_type TEXT NOT NULL CHECK (owner_type IN ('research1', 'research2')),
  owner_id TEXT NOT NULL CHECK (length(trim(owner_id)) > 0),
  cutoff_at DATETIME NOT NULL,
  query_text TEXT NOT NULL CHECK (length(trim(query_text)) > 0),
  experimental_enabled INTEGER NOT NULL CHECK (experimental_enabled IN (0, 1)),
  created_at DATETIME NOT NULL
)`

const knowledgeRetrievalHitsTableSQL = `CREATE TABLE IF NOT EXISTS knowledge_retrieval_hits (
  retrieval_hit_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(retrieval_hit_id)) > 0),
  retrieval_run_id TEXT NOT NULL CHECK (length(trim(retrieval_run_id)) > 0),
  version_id TEXT NOT NULL CHECK (length(trim(version_id)) > 0),
  rank INTEGER NOT NULL CHECK (rank >= 1),
  score REAL NOT NULL,
  adopted INTEGER NOT NULL DEFAULT 0 CHECK (adopted IN (0, 1)),
  adoption_reason TEXT,
  verification_status TEXT NOT NULL CHECK (verification_status IN ('unverified', 'verified', 'rejected')),
  verification_reason TEXT,
  evidence_set_id TEXT,
  evidence_item_id TEXT,
  evidence_refs_json TEXT NOT NULL DEFAULT '[]',
  created_at DATETIME NOT NULL,
  UNIQUE (retrieval_run_id, rank),
  UNIQUE (retrieval_run_id, version_id)
)`

const knowledgeDocumentFTSTableSQL = `CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_document_fts USING fts5(
  version_id UNINDEXED,
  document_id UNINDEXED,
  title,
  content_text,
  tokenize = 'unicode61'
)`

var knowledgeTableSQL = []string{
	knowledgeDocumentsTableSQL,
	knowledgeDocumentVersionsTableSQL,
	knowledgeVersionStatesTableSQL,
	knowledgeMemoryCandidatesTableSQL,
	knowledgeRetrievalRunsTableSQL,
	knowledgeRetrievalHitsTableSQL,
}

var knowledgeIndexSQL = []string{
	`CREATE INDEX IF NOT EXISTS idx_knowledge_documents_updated_at ON knowledge_documents(updated_at)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_document_versions_document_version ON knowledge_document_versions(document_id, version_no)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_document_versions_document_created ON knowledge_document_versions(document_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_document_versions_hash ON knowledge_document_versions(content_sha256)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_version_states_status_updated ON knowledge_version_states(status, updated_at)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_memory_candidates_status_created ON knowledge_memory_candidates(status, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_memory_candidates_owner_created ON knowledge_memory_candidates(source_owner_type, source_owner_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_retrieval_runs_owner_created ON knowledge_retrieval_runs(owner_type, owner_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_retrieval_runs_cutoff ON knowledge_retrieval_runs(cutoff_at)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_retrieval_hits_version ON knowledge_retrieval_hits(version_id)`,
	`CREATE INDEX IF NOT EXISTS idx_knowledge_retrieval_hits_run_adopted ON knowledge_retrieval_hits(retrieval_run_id, adopted, rank)`,
}

var knowledgeTriggerSQL = []string{
	`CREATE TRIGGER IF NOT EXISTS identity_knowledge_documents_update
BEFORE UPDATE ON knowledge_documents
WHEN NEW.document_id IS NOT OLD.document_id
  OR NEW.document_type IS NOT OLD.document_type
  OR NEW.origin_type IS NOT OLD.origin_type
  OR NEW.source_owner_type IS NOT OLD.source_owner_type
  OR NEW.source_owner_id IS NOT OLD.source_owner_id
  OR NEW.created_by_user_id IS NOT OLD.created_by_user_id
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'knowledge document identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_documents_delete
BEFORE DELETE ON knowledge_documents
BEGIN
  SELECT RAISE(ABORT, 'knowledge document cannot be deleted');
END`,
	`CREATE TRIGGER IF NOT EXISTS guard_knowledge_document_versions_document
BEFORE INSERT ON knowledge_document_versions
WHEN NOT EXISTS (SELECT 1 FROM knowledge_documents WHERE document_id = NEW.document_id)
BEGIN
  SELECT RAISE(ABORT, 'knowledge document version requires an existing document');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_document_versions_update
BEFORE UPDATE ON knowledge_document_versions
BEGIN
  SELECT RAISE(ABORT, 'knowledge document version is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_document_versions_delete
BEFORE DELETE ON knowledge_document_versions
BEGIN
  SELECT RAISE(ABORT, 'knowledge document version is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS insert_knowledge_document_fts
AFTER INSERT ON knowledge_document_versions
BEGIN
  INSERT INTO knowledge_document_fts(version_id, document_id, title, content_text)
  SELECT NEW.version_id, NEW.document_id, title, NEW.content_text
  FROM knowledge_documents
  WHERE document_id = NEW.document_id;
END`,
	`CREATE TRIGGER IF NOT EXISTS sync_knowledge_document_fts_title
AFTER UPDATE OF title ON knowledge_documents
BEGIN
  UPDATE knowledge_document_fts SET title = NEW.title WHERE document_id = NEW.document_id;
END`,
	`CREATE TRIGGER IF NOT EXISTS initial_knowledge_version_states_draft
BEFORE INSERT ON knowledge_version_states
WHEN NEW.status != 'draft'
BEGIN
  SELECT RAISE(ABORT, 'knowledge version state must begin as draft');
END`,
	`CREATE TRIGGER IF NOT EXISTS guard_knowledge_version_states_version
BEFORE INSERT ON knowledge_version_states
WHEN NOT EXISTS (SELECT 1 FROM knowledge_document_versions WHERE version_id = NEW.version_id)
BEGIN
  SELECT RAISE(ABORT, 'knowledge version state requires an existing version');
END`,
	`CREATE TRIGGER IF NOT EXISTS transition_knowledge_version_states_update
BEFORE UPDATE ON knowledge_version_states
WHEN NEW.state_id IS NOT OLD.state_id
  OR NEW.version_id IS NOT OLD.version_id
  OR NEW.created_at IS NOT OLD.created_at
  OR NOT ((OLD.status = 'draft' AND NEW.status IN ('approved', 'rejected'))
       OR (OLD.status = 'approved' AND NEW.status = 'superseded'))
  OR (OLD.status = 'approved' AND (NEW.approved_by_actor_type IS NOT OLD.approved_by_actor_type
       OR NEW.approved_by_user_id IS NOT OLD.approved_by_user_id
       OR NEW.approval_reason IS NOT OLD.approval_reason
       OR NEW.approved_at IS NOT OLD.approved_at))
BEGIN
  SELECT RAISE(ABORT, 'invalid knowledge version state transition');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_version_states_delete
BEFORE DELETE ON knowledge_version_states
BEGIN
  SELECT RAISE(ABORT, 'knowledge version state cannot be deleted');
END`,
	`CREATE TRIGGER IF NOT EXISTS initial_knowledge_memory_candidates_draft
BEFORE INSERT ON knowledge_memory_candidates
WHEN NEW.status != 'draft'
BEGIN
  SELECT RAISE(ABORT, 'knowledge memory candidate must begin as draft');
END`,
	`CREATE TRIGGER IF NOT EXISTS transition_knowledge_memory_candidates_update
BEFORE UPDATE ON knowledge_memory_candidates
WHEN NEW.candidate_id IS NOT OLD.candidate_id
  OR NEW.source_owner_type IS NOT OLD.source_owner_type
  OR NEW.source_owner_id IS NOT OLD.source_owner_id
  OR NEW.title IS NOT OLD.title
  OR NEW.content_text IS NOT OLD.content_text
  OR NEW.content_sha256 IS NOT OLD.content_sha256
  OR NEW.proposed_by_actor_type IS NOT OLD.proposed_by_actor_type
  OR NEW.proposed_by_actor_id IS NOT OLD.proposed_by_actor_id
  OR NEW.created_at IS NOT OLD.created_at
  OR OLD.status != 'draft'
  OR NEW.status NOT IN ('approved', 'rejected')
BEGIN
  SELECT RAISE(ABORT, 'invalid knowledge memory candidate transition');
END`,
	`CREATE TRIGGER IF NOT EXISTS guard_knowledge_memory_candidates_approved_version
BEFORE UPDATE OF approved_version_id ON knowledge_memory_candidates
WHEN NEW.approved_version_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM knowledge_document_versions WHERE version_id = NEW.approved_version_id)
BEGIN
  SELECT RAISE(ABORT, 'knowledge memory candidate approved version does not exist');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_memory_candidates_delete
BEFORE DELETE ON knowledge_memory_candidates
BEGIN
  SELECT RAISE(ABORT, 'knowledge memory candidate cannot be deleted');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_retrieval_runs_update
BEFORE UPDATE ON knowledge_retrieval_runs
BEGIN
  SELECT RAISE(ABORT, 'knowledge retrieval run is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_retrieval_runs_delete
BEFORE DELETE ON knowledge_retrieval_runs
BEGIN
  SELECT RAISE(ABORT, 'knowledge retrieval run is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS guard_knowledge_retrieval_hits_run_version
BEFORE INSERT ON knowledge_retrieval_hits
WHEN NOT EXISTS (SELECT 1 FROM knowledge_retrieval_runs WHERE retrieval_run_id = NEW.retrieval_run_id)
  OR NOT EXISTS (SELECT 1 FROM knowledge_document_versions WHERE version_id = NEW.version_id)
BEGIN
  SELECT RAISE(ABORT, 'knowledge retrieval hit requires an existing run and version');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_retrieval_hits_update
BEFORE UPDATE ON knowledge_retrieval_hits
BEGIN
  SELECT RAISE(ABORT, 'knowledge retrieval hit is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_knowledge_retrieval_hits_delete
BEFORE DELETE ON knowledge_retrieval_hits
BEGIN
  SELECT RAISE(ABORT, 'knowledge retrieval hit is immutable');
END`,
}

func mainMigrationV19Definition() string {
	parts := append([]string{}, knowledgeTableSQL...)
	parts = append(parts, knowledgeDocumentFTSTableSQL)
	parts = append(parts, knowledgeIndexSQL...)
	parts = append(parts, knowledgeTriggerSQL...)
	return strings.Join(parts, ";\n\n") + ";\n"
}

func applyKnowledgeSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, statement := range knowledgeTableSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create knowledge table: %w", err)
		}
	}
	if err := tx.Exec(knowledgeDocumentFTSTableSQL).Error; err != nil {
		return fmt.Errorf("create knowledge FTS5 table: %w", err)
	}
	for _, statement := range knowledgeIndexSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create knowledge index: %w", err)
		}
	}
	for _, statement := range knowledgeTriggerSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create knowledge trigger: %w", err)
		}
	}
	return verifyMainSchema19Runtime(tx)
}

func verifyMainSchema19Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	tableNames := []string{
		"knowledge_documents",
		"knowledge_document_versions",
		"knowledge_version_states",
		"knowledge_memory_candidates",
		"knowledge_retrieval_runs",
		"knowledge_retrieval_hits",
	}
	for index, statement := range knowledgeTableSQL {
		if err := verifySQLiteSchemaObject(database, "table", tableNames[index], statement); err != nil {
			return fmt.Errorf("verify main schema 19 table %s: %w", tableNames[index], err)
		}
	}
	storedFTSSQL := strings.Replace(knowledgeDocumentFTSTableSQL, "CREATE VIRTUAL TABLE IF NOT EXISTS", "CREATE VIRTUAL TABLE", 1)
	if err := verifySQLiteSchemaObject(database, "table", "knowledge_document_fts", storedFTSSQL); err != nil {
		return fmt.Errorf("verify main schema 19 FTS table: %w", err)
	}
	for _, statement := range knowledgeIndexSQL {
		name := sqliteSchemaObjectName(statement)
		if err := verifySQLiteSchemaObject(database, "index", name, sqliteStoredIndexSQL(statement)); err != nil {
			return fmt.Errorf("verify main schema 19 index %s: %w", name, err)
		}
	}
	triggerNames := []string{
		"identity_knowledge_documents_update",
		"immutable_knowledge_documents_delete",
		"guard_knowledge_document_versions_document",
		"immutable_knowledge_document_versions_update",
		"immutable_knowledge_document_versions_delete",
		"insert_knowledge_document_fts",
		"sync_knowledge_document_fts_title",
		"initial_knowledge_version_states_draft",
		"guard_knowledge_version_states_version",
		"transition_knowledge_version_states_update",
		"immutable_knowledge_version_states_delete",
		"initial_knowledge_memory_candidates_draft",
		"transition_knowledge_memory_candidates_update",
		"guard_knowledge_memory_candidates_approved_version",
		"immutable_knowledge_memory_candidates_delete",
		"immutable_knowledge_retrieval_runs_update",
		"immutable_knowledge_retrieval_runs_delete",
		"guard_knowledge_retrieval_hits_run_version",
		"immutable_knowledge_retrieval_hits_update",
		"immutable_knowledge_retrieval_hits_delete",
	}
	for index, statement := range knowledgeTriggerSQL {
		name := triggerNames[index]
		if err := verifySQLiteSchemaObject(database, "trigger", name, sqliteStoredIndexSQL(statement)); err != nil {
			return fmt.Errorf("verify main schema 19 trigger %s: %w", name, err)
		}
	}
	return nil
}
