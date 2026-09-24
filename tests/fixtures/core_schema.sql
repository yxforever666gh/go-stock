-- Frozen from the synthetic Go 5.2.5 schema oracle; contains no user data.
CREATE TABLE `ai_config` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`sort` integer,`name` text,`base_url` text,`api_key` text,`model_name` text,`api_protocol` text DEFAULT "chat_completions",`max_tokens` integer,`temperature` real,`time_out` integer,`http_proxy` text,`http_proxy_enabled` numeric, disabled numeric NOT NULL DEFAULT 0, `owner` text NOT NULL DEFAULT "global", `archived_at` datetime);

CREATE TABLE research_audit_payloads (
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
);

CREATE TABLE research_audit_prompt_versions (
  prompt_version_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(prompt_version_id)) > 0),
  research_scope TEXT NOT NULL CHECK (research_scope IN ('research1', 'research2')),
  phase TEXT NOT NULL CHECK (length(trim(phase)) > 0),
  version TEXT NOT NULL CHECK (length(trim(version)) > 0),
  template_codec TEXT NOT NULL DEFAULT 'gzip' CHECK (template_codec = 'gzip'),
  template_blob BLOB NOT NULL CHECK (typeof(template_blob) = 'blob' AND length(template_blob) > 0),
  template_sha256 CHAR(64) NOT NULL CHECK (length(template_sha256) = 64 AND template_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  created_at DATETIME NOT NULL,
  UNIQUE (research_scope, phase, version)
);

CREATE TABLE research_audit_run_states (
  owner_type TEXT NOT NULL CHECK (owner_type IN ('research1', 'research2', 'replay')),
  owner_id TEXT NOT NULL CHECK (length(trim(owner_id)) > 0),
  status TEXT NOT NULL CHECK (status IN ('capturing', 'complete', 'failed', 'legacy_unavailable')),
  payload_count INTEGER NOT NULL DEFAULT 0 CHECK (payload_count >= 0),
  last_error TEXT,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE (owner_type, owner_id)
);

CREATE TABLE research_replay_results (
  replay_result_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(replay_result_id)) > 0),
  replay_id TEXT NOT NULL CHECK (length(trim(replay_id)) > 0),
  result_codec TEXT NOT NULL DEFAULT 'gzip' CHECK (result_codec = 'gzip'),
  result_blob BLOB NOT NULL CHECK (typeof(result_blob) = 'blob' AND length(result_blob) > 0),
  result_sha256 CHAR(64) NOT NULL CHECK (length(result_sha256) = 64 AND result_sha256 NOT GLOB '*[^0-9A-Fa-f]*'),
  diff_summary_json TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL,
  UNIQUE (replay_id)
);

CREATE TABLE research_replays (
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
);

CREATE TABLE `research_settings` (`center` text,`config_json` text NOT NULL,`revision` integer NOT NULL,PRIMARY KEY (`center`),CONSTRAINT `research_revision` CHECK (revision > 0),CONSTRAINT `research_center` CHECK (center IN ('research1','research2')));

CREATE TABLE `settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`created_at` datetime,`updated_at` datetime,`deleted_at` datetime,`tushare_token` text,`local_push_enable` numeric,`ding_push_enable` numeric,`ding_robot` text,`yield_email_enable` numeric,`yield_email_to` text,`yield_email_from` text,`yield_email_smtp_host` text,`yield_email_smtp_port` integer,`yield_email_smtp_username` text,`yield_email_smtp_password` text,`yield_email_cron_enabled` numeric,`yield_email_cron_times` text,`market_summary_email_enable` numeric,`update_basic_info_on_start` numeric,`refresh_interval` integer,`open_ai_enable` numeric,`prompt` text,`check_update` numeric,`question_template` text,`crawl_time_out` integer,`k_days` integer,`enable_danmu` numeric,`browser_path` text,`enable_news` numeric,`dark_theme` numeric,`browser_pool_size` integer,`enable_fund` numeric,`enable_push_news` numeric,`enable_only_push_red_news` numeric,`http_proxy` text,`http_proxy_enabled` numeric,`force_no_proxy_for_fetch` numeric DEFAULT true,`enable_agent` numeric,`qgqp_b_id` text,`market_summary_cron_enabled` numeric DEFAULT true,`market_summary_cron_times` text DEFAULT "09:40,11:30,14:30",`minute_provider_mode` text DEFAULT "public",`minute_long_history_hint_enabled` numeric DEFAULT true,`private_minute_enabled` numeric,`private_minute_base_url` text,`private_minute_api_key` text,`private_minute_timeout_sec` integer,`private_minute_min_interval` integer,`private_minute_proxy_mode` text DEFAULT "disable",`private_minute_level` text DEFAULT "1min",`akshare_enabled` numeric DEFAULT true,`sina_minute_enabled` numeric DEFAULT true,`tencent_minute_enabled` numeric DEFAULT true,`eastmoney_minute_enabled` numeric DEFAULT true,`akshare_minute_source_mode` text DEFAULT "auto", `ai_analysis_enabled` numeric DEFAULT true, `ai_capital_deployment_enabled` numeric DEFAULT true, `ai_target_capital_utilization` real DEFAULT 0.9, `ai_max_immediate_buys_per_run` integer DEFAULT 2, `ai_reanalysis_interval_minutes` integer DEFAULT 30, `research2_auto_enabled` numeric DEFAULT true, `research2_email_enabled` numeric, `research2_email_to` text, `research2_email_from` text, `research2_email_smtp_host` text, `research2_email_smtp_port` integer, `research2_email_smtp_user` text, `research2_email_smtp_pass` text, `research2_email_slots` text NOT NULL DEFAULT "[]", `ai_analysis_config_id` integer, `ai_analysis_times` text DEFAULT "09:30,11:30,14:30", `ai_review_start_time` text DEFAULT "09:50", `ai_review_interval_minutes` integer DEFAULT 15, `minute_provider_order` text DEFAULT "tencent,sina,akshare,private", `experimental_evidence_enabled` numeric DEFAULT false);

CREATE INDEX idx_ai_config_owner_archived ON ai_config(owner, archived_at);

CREATE INDEX `idx_ai_config_sort` ON `ai_config`(`sort`);

CREATE UNIQUE INDEX idx_research_audit_payloads_owner_call_attempt ON research_audit_payloads(owner_type, owner_id, call_sequence, attempt);

CREATE INDEX idx_research_audit_payloads_owner_phase_created ON research_audit_payloads(owner_type, owner_id, phase, created_at);

CREATE INDEX idx_research_audit_payloads_prompt_version ON research_audit_payloads(prompt_version_id);

CREATE INDEX idx_research_audit_prompt_versions_scope_phase_created ON research_audit_prompt_versions(research_scope, phase, created_at);

CREATE INDEX idx_research_audit_run_states_status_updated ON research_audit_run_states(status, updated_at);

CREATE INDEX idx_research_replays_model_cutoff ON research_replays(model_config_id, cutoff_at);

CREATE INDEX idx_research_replays_source_created ON research_replays(source_owner_type, source_owner_id, created_at);

CREATE INDEX idx_research_replays_status_created ON research_replays(status, created_at);

CREATE INDEX `idx_settings_deleted_at` ON `settings`(`deleted_at`);

CREATE TRIGGER identity_research_audit_run_states_update
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
END;

CREATE TRIGGER identity_research_replays_update
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
END;

CREATE TRIGGER immutable_research_audit_payloads_delete
BEFORE DELETE ON research_audit_payloads
BEGIN
  SELECT RAISE(ABORT, 'research audit payload is immutable');
END;

CREATE TRIGGER immutable_research_audit_payloads_update
BEFORE UPDATE ON research_audit_payloads
BEGIN
  SELECT RAISE(ABORT, 'research audit payload is immutable');
END;

CREATE TRIGGER immutable_research_audit_prompt_versions_delete
BEFORE DELETE ON research_audit_prompt_versions
BEGIN
  SELECT RAISE(ABORT, 'research audit prompt version is immutable');
END;

CREATE TRIGGER immutable_research_audit_prompt_versions_update
BEFORE UPDATE ON research_audit_prompt_versions
BEGIN
  SELECT RAISE(ABORT, 'research audit prompt version is immutable');
END;

CREATE TRIGGER immutable_research_audit_run_states_delete
BEFORE DELETE ON research_audit_run_states
BEGIN
  SELECT RAISE(ABORT, 'research audit run state identity is immutable');
END;

CREATE TRIGGER immutable_research_replay_results_delete
BEFORE DELETE ON research_replay_results
BEGIN
  SELECT RAISE(ABORT, 'research replay result is immutable');
END;

CREATE TRIGGER immutable_research_replay_results_update
BEFORE UPDATE ON research_replay_results
BEGIN
  SELECT RAISE(ABORT, 'research replay result is immutable');
END;

CREATE TRIGGER immutable_research_replays_delete
BEFORE DELETE ON research_replays
BEGIN
  SELECT RAISE(ABORT, 'research replay is immutable except for status, time and error fields');
END;

CREATE TABLE research_evidence_items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  evidence_item_id TEXT NOT NULL,
  evidence_set_id TEXT NOT NULL,
  source_id TEXT NOT NULL,
  source_name TEXT NOT NULL,
  source_ref TEXT,
  category TEXT NOT NULL,
  entity_type TEXT,
  entity_id TEXT,
  event_at DATETIME,
  available_at DATETIME,
  collected_at DATETIME NOT NULL,
  status TEXT NOT NULL,
  summary TEXT,
  payload BLOB NOT NULL DEFAULT X'',
  payload_encoding TEXT NOT NULL DEFAULT 'identity',
  content_hash TEXT NOT NULL,
  error_message TEXT,
  created_at DATETIME NOT NULL
);

CREATE TABLE research_evidence_sets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  evidence_set_id TEXT NOT NULL,
  owner_type TEXT NOT NULL,
  owner_id TEXT NOT NULL,
  cutoff_at DATETIME NOT NULL,
  collector_version TEXT NOT NULL,
  evidence_profile_version TEXT NOT NULL,
  status TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  frozen_at DATETIME,
  created_at DATETIME NOT NULL
);

CREATE INDEX idx_research_evidence_items_available ON research_evidence_items(evidence_set_id, available_at);

CREATE INDEX idx_research_evidence_items_category ON research_evidence_items(category);

CREATE INDEX idx_research_evidence_items_collected ON research_evidence_items(collected_at);

CREATE INDEX idx_research_evidence_items_entity ON research_evidence_items(entity_type, entity_id);

CREATE UNIQUE INDEX idx_research_evidence_items_public_id ON research_evidence_items(evidence_item_id);

CREATE UNIQUE INDEX idx_research_evidence_items_set_source ON research_evidence_items(evidence_set_id, source_id);

CREATE INDEX idx_research_evidence_sets_cutoff ON research_evidence_sets(cutoff_at);

CREATE INDEX idx_research_evidence_sets_owner ON research_evidence_sets(owner_type, owner_id);

CREATE UNIQUE INDEX idx_research_evidence_sets_public_id ON research_evidence_sets(evidence_set_id);
