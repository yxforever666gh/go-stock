package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const researchAuditPromptVersionsTableSQL = `CREATE TABLE IF NOT EXISTS research_audit_prompt_versions (
  prompt_version_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(prompt_version_id)) > 0),
  research_scope TEXT NOT NULL CHECK (research_scope IN ('research1', 'research2')),
  phase TEXT NOT NULL CHECK (length(trim(phase)) > 0),
  version TEXT NOT NULL CHECK (length(trim(version)) > 0),
  template_codec TEXT NOT NULL DEFAULT 'gzip' CHECK (template_codec = 'gzip'),
  template_blob BLOB NOT NULL CHECK (typeof(template_blob) = 'blob' AND length(template_blob) > 0),
  template_sha256 CHAR(64) NOT NULL CHECK (length(template_sha256) = 64 AND template_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  created_at DATETIME NOT NULL,
  UNIQUE (research_scope, phase, version)
)`

const researchAuditPayloadsTableSQL = `CREATE TABLE IF NOT EXISTS research_audit_payloads (
  payload_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(payload_id)) > 0),
  owner_type TEXT NOT NULL CHECK (owner_type IN ('research1', 'research2', 'replay')),
  owner_id TEXT NOT NULL CHECK (length(trim(owner_id)) > 0),
  prompt_version_id TEXT CHECK (prompt_version_id IS NULL OR length(trim(prompt_version_id)) > 0),
  phase TEXT NOT NULL CHECK (length(trim(phase)) > 0),
  call_sequence INTEGER NOT NULL CHECK (call_sequence >= 1),
  attempt INTEGER NOT NULL CHECK (attempt >= 1),
  provider_name TEXT NOT NULL CHECK (length(trim(provider_name)) > 0),
  model_name TEXT NOT NULL CHECK (length(trim(model_name)) > 0),
  model_parameters_json TEXT NOT NULL DEFAULT '{}',
  cutoff_at DATETIME,
  final_prompt_codec TEXT NOT NULL DEFAULT 'gzip' CHECK (final_prompt_codec = 'gzip'),
  final_prompt_blob BLOB NOT NULL CHECK (typeof(final_prompt_blob) = 'blob' AND length(final_prompt_blob) > 0),
  final_prompt_sha256 CHAR(64) NOT NULL CHECK (length(final_prompt_sha256) = 64 AND final_prompt_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  evidence_codec TEXT NOT NULL DEFAULT 'gzip' CHECK (evidence_codec = 'gzip'),
  evidence_blob BLOB NOT NULL CHECK (typeof(evidence_blob) = 'blob' AND length(evidence_blob) > 0),
  evidence_sha256 CHAR(64) NOT NULL CHECK (length(evidence_sha256) = 64 AND evidence_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  tools_json TEXT NOT NULL DEFAULT '[]',
  raw_response_codec TEXT CHECK (raw_response_codec IS NULL OR raw_response_codec = 'gzip'),
  raw_response_blob BLOB,
  raw_response_sha256 CHAR(64),
  repaired_response_codec TEXT CHECK (repaired_response_codec IS NULL OR repaired_response_codec = 'gzip'),
  repaired_response_blob BLOB,
  repaired_response_sha256 CHAR(64),
  repair_log_codec TEXT CHECK (repair_log_codec IS NULL OR repair_log_codec = 'gzip'),
  repair_log_blob BLOB,
  repair_log_sha256 CHAR(64),
  redaction_manifest_json TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL,
  UNIQUE (owner_type, owner_id, call_sequence, attempt),
  CHECK ((raw_response_codec IS NULL AND (raw_response_blob IS NULL OR length(raw_response_blob) = 0) AND raw_response_sha256 IS NULL) OR
         (raw_response_codec = 'gzip' AND typeof(raw_response_blob) = 'blob' AND length(raw_response_blob) > 0 AND length(raw_response_sha256) = 64 AND raw_response_sha256 NOT GLOB '*[^0-9A-Fa-f]*')),
  CHECK ((repaired_response_codec IS NULL AND (repaired_response_blob IS NULL OR length(repaired_response_blob) = 0) AND repaired_response_sha256 IS NULL) OR
         (repaired_response_codec = 'gzip' AND typeof(repaired_response_blob) = 'blob' AND length(repaired_response_blob) > 0 AND length(repaired_response_sha256) = 64 AND repaired_response_sha256 NOT GLOB '*[^0-9A-Fa-f]*')),
  CHECK ((repair_log_codec IS NULL AND (repair_log_blob IS NULL OR length(repair_log_blob) = 0) AND repair_log_sha256 IS NULL) OR
         (repair_log_codec = 'gzip' AND typeof(repair_log_blob) = 'blob' AND length(repair_log_blob) > 0 AND length(repair_log_sha256) = 64 AND repair_log_sha256 NOT GLOB '*[^0-9A-Fa-f]*'))
)`

const researchAuditRunStatesTableSQL = `CREATE TABLE IF NOT EXISTS research_audit_run_states (
  owner_type TEXT NOT NULL CHECK (owner_type IN ('research1', 'research2', 'replay')),
  owner_id TEXT NOT NULL CHECK (length(trim(owner_id)) > 0),
  status TEXT NOT NULL CHECK (status IN ('capturing', 'complete', 'failed', 'legacy_unavailable')),
  payload_count INTEGER NOT NULL DEFAULT 0 CHECK (payload_count >= 0),
  last_error TEXT,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE (owner_type, owner_id)
)`

const researchReplaysTableSQL = `CREATE TABLE IF NOT EXISTS research_replays (
  replay_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(replay_id)) > 0),
  source_owner_type TEXT NOT NULL CHECK (source_owner_type IN ('research1', 'research2')),
  source_owner_id TEXT NOT NULL CHECK (length(trim(source_owner_id)) > 0),
  model_config_id INTEGER NOT NULL CHECK (model_config_id >= 1),
  status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed')),
  cutoff_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  started_at DATETIME,
  completed_at DATETIME,
  last_error TEXT,
  CHECK ((status = 'queued' AND started_at IS NULL AND completed_at IS NULL) OR
         (status = 'running' AND started_at IS NOT NULL AND completed_at IS NULL) OR
         (status IN ('completed', 'failed') AND started_at IS NOT NULL AND completed_at IS NOT NULL))
)`

const researchReplayResultsTableSQL = `CREATE TABLE IF NOT EXISTS research_replay_results (
  replay_result_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(replay_result_id)) > 0),
  replay_id TEXT NOT NULL CHECK (length(trim(replay_id)) > 0),
  result_codec TEXT NOT NULL DEFAULT 'gzip' CHECK (result_codec = 'gzip'),
  result_blob BLOB NOT NULL CHECK (typeof(result_blob) = 'blob' AND length(result_blob) > 0),
  result_sha256 CHAR(64) NOT NULL CHECK (length(result_sha256) = 64 AND result_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  diff_summary_json TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL,
  UNIQUE (replay_id)
)`

var researchAuditTableSQL = []string{
	researchAuditPromptVersionsTableSQL,
	researchAuditPayloadsTableSQL,
	researchAuditRunStatesTableSQL,
	researchReplaysTableSQL,
	researchReplayResultsTableSQL,
}

var researchAuditIndexSQL = []string{
	`CREATE INDEX IF NOT EXISTS idx_research_audit_prompt_versions_scope_phase_created ON research_audit_prompt_versions(research_scope, phase, created_at)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_research_audit_payloads_owner_call_attempt ON research_audit_payloads(owner_type, owner_id, call_sequence, attempt)`,
	`CREATE INDEX IF NOT EXISTS idx_research_audit_payloads_owner_phase_created ON research_audit_payloads(owner_type, owner_id, phase, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_research_audit_payloads_prompt_version ON research_audit_payloads(prompt_version_id)`,
	`CREATE INDEX IF NOT EXISTS idx_research_audit_run_states_status_updated ON research_audit_run_states(status, updated_at)`,
	`CREATE INDEX IF NOT EXISTS idx_research_replays_source_created ON research_replays(source_owner_type, source_owner_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_research_replays_status_created ON research_replays(status, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_research_replays_model_cutoff ON research_replays(model_config_id, cutoff_at)`,
}

var researchAuditTriggerSQL = []string{
	`CREATE TRIGGER IF NOT EXISTS immutable_research_audit_prompt_versions_update
BEFORE UPDATE ON research_audit_prompt_versions
BEGIN
  SELECT RAISE(ABORT, 'research audit prompt version is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_research_audit_prompt_versions_delete
BEFORE DELETE ON research_audit_prompt_versions
BEGIN
  SELECT RAISE(ABORT, 'research audit prompt version is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_research_audit_payloads_update
BEFORE UPDATE ON research_audit_payloads
BEGIN
  SELECT RAISE(ABORT, 'research audit payload is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_research_audit_payloads_delete
BEFORE DELETE ON research_audit_payloads
BEGIN
  SELECT RAISE(ABORT, 'research audit payload is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_research_audit_run_states_update
BEFORE UPDATE ON research_audit_run_states
WHEN NEW.owner_type IS NOT OLD.owner_type
  OR NEW.owner_id IS NOT OLD.owner_id
  OR NEW.created_at IS NOT OLD.created_at
  OR (OLD.status != 'capturing' AND NEW.status IS NOT OLD.status)
  OR (OLD.status = 'capturing' AND NEW.status NOT IN ('capturing', 'complete', 'failed'))
  OR NEW.payload_count < OLD.payload_count
  OR (OLD.status != 'capturing' AND NEW.payload_count IS NOT OLD.payload_count)
BEGIN
  SELECT RAISE(ABORT, 'research audit run state identity and terminal state are immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_research_audit_run_states_delete
BEFORE DELETE ON research_audit_run_states
BEGIN
  SELECT RAISE(ABORT, 'research audit run state identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_research_replays_update
BEFORE UPDATE ON research_replays
WHEN NEW.replay_id IS NOT OLD.replay_id
  OR NEW.source_owner_type IS NOT OLD.source_owner_type
  OR NEW.source_owner_id IS NOT OLD.source_owner_id
  OR NEW.model_config_id IS NOT OLD.model_config_id
  OR NEW.cutoff_at IS NOT OLD.cutoff_at
  OR NEW.created_at IS NOT OLD.created_at
  OR (OLD.status = 'queued' AND NEW.status NOT IN ('queued', 'running'))
  OR (OLD.status = 'running' AND NEW.status NOT IN ('running', 'completed', 'failed'))
  OR (OLD.status IN ('completed', 'failed') AND NEW.status IS NOT OLD.status)
  OR (OLD.started_at IS NOT NULL AND NEW.started_at IS NOT OLD.started_at)
  OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS NOT OLD.completed_at)
BEGIN
  SELECT RAISE(ABORT, 'research replay identity and terminal state are immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_research_replays_delete
BEFORE DELETE ON research_replays
BEGIN
  SELECT RAISE(ABORT, 'research replay is immutable except for status, time and error fields');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_research_replay_results_update
BEFORE UPDATE ON research_replay_results
BEGIN
  SELECT RAISE(ABORT, 'research replay result is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_research_replay_results_delete
BEFORE DELETE ON research_replay_results
BEGIN
  SELECT RAISE(ABORT, 'research replay result is immutable');
END`,
}

func mainMigrationV18Definition() string {
	parts := append([]string{}, researchAuditTableSQL...)
	parts = append(parts, researchAuditIndexSQL...)
	parts = append(parts, researchAuditTriggerSQL...)
	return strings.Join(parts, ";\n\n") + ";\n"
}

func applyResearchAuditSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, statement := range researchAuditTableSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create research audit table: %w", err)
		}
	}
	for _, statement := range researchAuditIndexSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create research audit index: %w", err)
		}
	}
	for _, statement := range researchAuditTriggerSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create research audit trigger: %w", err)
		}
	}
	return verifyMainSchema18Runtime(tx)
}

func verifyMainSchema18Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	tableNames := []string{
		"research_audit_prompt_versions",
		"research_audit_payloads",
		"research_audit_run_states",
		"research_replays",
		"research_replay_results",
	}
	for index, statement := range researchAuditTableSQL {
		if err := verifySQLiteSchemaObject(database, "table", tableNames[index], statement); err != nil {
			return fmt.Errorf("verify main schema 18 table %s: %w", tableNames[index], err)
		}
	}
	for _, statement := range researchAuditIndexSQL {
		name := sqliteSchemaObjectName(statement)
		if err := verifySQLiteSchemaObject(database, "index", name, sqliteStoredIndexSQL(statement)); err != nil {
			return fmt.Errorf("verify main schema 18 index %s: %w", name, err)
		}
	}
	triggerNames := []string{
		"immutable_research_audit_prompt_versions_update",
		"immutable_research_audit_prompt_versions_delete",
		"immutable_research_audit_payloads_update",
		"immutable_research_audit_payloads_delete",
		"identity_research_audit_run_states_update",
		"immutable_research_audit_run_states_delete",
		"identity_research_replays_update",
		"immutable_research_replays_delete",
		"immutable_research_replay_results_update",
		"immutable_research_replay_results_delete",
	}
	for index, statement := range researchAuditTriggerSQL {
		name := triggerNames[index]
		if err := verifySQLiteSchemaObject(database, "trigger", name, sqliteStoredIndexSQL(statement)); err != nil {
			return fmt.Errorf("verify main schema 18 trigger %s: %w", name, err)
		}
	}
	return nil
}
