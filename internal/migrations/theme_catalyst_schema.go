package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const marketThemesTableSQL = `CREATE TABLE IF NOT EXISTS market_themes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  theme_id TEXT NOT NULL,
  canonical_name TEXT NOT NULL,
  normalized_name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL CHECK (status IN ('active', 'archived')),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const marketThemeAliasesTableSQL = `CREATE TABLE IF NOT EXISTS market_theme_aliases (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  alias_id TEXT NOT NULL,
  theme_id TEXT NOT NULL,
  alias TEXT NOT NULL,
  normalized_alias TEXT NOT NULL,
  source TEXT,
  created_at DATETIME NOT NULL
)`

const marketThemeDailySnapshotsTableSQL = `CREATE TABLE IF NOT EXISTS market_theme_daily_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  snapshot_id TEXT NOT NULL,
  theme_id TEXT NOT NULL,
  trade_date TEXT NOT NULL,
  cycle_no INTEGER NOT NULL CHECK (cycle_no >= 1),
  lifecycle_stage TEXT NOT NULL CHECK (lifecycle_stage IN ('观察', '发酵', '加速', '分歧', '退潮')),
  rank INTEGER NOT NULL CHECK (rank >= 1),
  heat_score REAL NOT NULL CHECK (heat_score >= 0 AND heat_score <= 100),
  summary TEXT,
  observed_at DATETIME NOT NULL,
  frozen_at DATETIME NOT NULL,
  content_hash TEXT NOT NULL,
  constituent_count INTEGER NOT NULL DEFAULT 0 CHECK (constituent_count >= 0),
  catalyst_count INTEGER NOT NULL DEFAULT 0 CHECK (catalyst_count >= 0),
  conflicting_catalyst_count INTEGER NOT NULL DEFAULT 0 CHECK (conflicting_catalyst_count >= 0 AND conflicting_catalyst_count <= catalyst_count),
  created_at DATETIME NOT NULL
)`

const marketCatalystEventsTableSQL = `CREATE TABLE IF NOT EXISTS market_catalyst_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  catalyst_event_id TEXT NOT NULL,
  theme_id TEXT NOT NULL,
  event_fingerprint TEXT NOT NULL,
  event_type TEXT NOT NULL,
  title TEXT NOT NULL,
  summary TEXT,
  event_at DATETIME NOT NULL,
  first_available_at DATETIME,
  credibility_score INTEGER NOT NULL CHECK (credibility_score >= 0 AND credibility_score <= 100),
  status TEXT NOT NULL CHECK (status IN ('active', 'disputed', 'retracted', 'expired')),
  entity_keys_json TEXT NOT NULL DEFAULT '[]',
  created_at DATETIME NOT NULL
)`

const marketCatalystSourceClaimsTableSQL = `CREATE TABLE IF NOT EXISTS market_catalyst_source_claims (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_claim_id TEXT NOT NULL,
  catalyst_event_id TEXT NOT NULL,
  source_name TEXT NOT NULL,
  source_ref TEXT NOT NULL,
  source_ref_hash TEXT NOT NULL,
  stance TEXT NOT NULL CHECK (stance IN ('supports', 'contradicts', 'neutral')),
  source_credibility_score INTEGER NOT NULL CHECK (source_credibility_score >= 0 AND source_credibility_score <= 100),
  summary TEXT,
  claim_fingerprint TEXT NOT NULL,
  published_at DATETIME,
  available_at DATETIME,
  collected_at DATETIME NOT NULL,
  raw_payload_hash TEXT NOT NULL,
  created_at DATETIME NOT NULL
)`

const marketThemeSnapshotCatalystsTableSQL = `CREATE TABLE IF NOT EXISTS market_theme_snapshot_catalysts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  snapshot_id TEXT NOT NULL,
  catalyst_event_id TEXT NOT NULL,
  created_at DATETIME NOT NULL
)`

const marketThemeSnapshotConstituentsTableSQL = `CREATE TABLE IF NOT EXISTS market_theme_snapshot_constituents (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  constituent_id TEXT NOT NULL,
  snapshot_id TEXT NOT NULL,
  asset_type TEXT NOT NULL CHECK (asset_type IN ('stock', 'index', 'etf', 'fund')),
  market TEXT NOT NULL,
  code TEXT NOT NULL,
  name TEXT NOT NULL,
  role TEXT,
  rank INTEGER NOT NULL CHECK (rank >= 1),
  contribution_score REAL NOT NULL,
  created_at DATETIME NOT NULL
)`

const marketThemeEvidenceLinksTableSQL = `CREATE TABLE IF NOT EXISTS market_theme_evidence_links (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  link_id TEXT NOT NULL,
  theme_id TEXT NOT NULL,
  snapshot_id TEXT,
  catalyst_event_id TEXT,
  source_claim_id TEXT,
  evidence_item_id TEXT NOT NULL,
  link_type TEXT NOT NULL CHECK (link_type IN ('supports', 'contradicts', 'neutral')),
  created_at DATETIME NOT NULL
)`

var themeCatalystTableSQL = []string{
	marketThemesTableSQL,
	marketThemeAliasesTableSQL,
	marketThemeDailySnapshotsTableSQL,
	marketCatalystEventsTableSQL,
	marketCatalystSourceClaimsTableSQL,
	marketThemeSnapshotCatalystsTableSQL,
	marketThemeSnapshotConstituentsTableSQL,
	marketThemeEvidenceLinksTableSQL,
}

var themeCatalystIndexSQL = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_themes_theme_id ON market_themes(theme_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_themes_normalized_name ON market_themes(normalized_name)`,
	`CREATE INDEX IF NOT EXISTS idx_market_themes_status_updated_at ON market_themes(status, updated_at)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_aliases_alias_id ON market_theme_aliases(alias_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_aliases_normalized_alias ON market_theme_aliases(normalized_alias)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_aliases_theme_id ON market_theme_aliases(theme_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_daily_snapshots_snapshot_id ON market_theme_daily_snapshots(snapshot_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_daily_snapshots_theme_date ON market_theme_daily_snapshots(theme_id, trade_date)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_daily_snapshots_date_stage ON market_theme_daily_snapshots(trade_date, lifecycle_stage)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_daily_snapshots_theme_cycle_date ON market_theme_daily_snapshots(theme_id, cycle_no, trade_date)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_catalyst_events_public_id ON market_catalyst_events(catalyst_event_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_catalyst_events_theme_fingerprint ON market_catalyst_events(theme_id, event_fingerprint)`,
	`CREATE INDEX IF NOT EXISTS idx_market_catalyst_events_theme_event_at ON market_catalyst_events(theme_id, event_at)`,
	`CREATE INDEX IF NOT EXISTS idx_market_catalyst_events_available_status ON market_catalyst_events(first_available_at, status)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_catalyst_source_claims_public_id ON market_catalyst_source_claims(source_claim_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_catalyst_source_claims_event_source_ref ON market_catalyst_source_claims(catalyst_event_id, source_ref_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_market_catalyst_source_claims_event_available ON market_catalyst_source_claims(catalyst_event_id, available_at)`,
	`CREATE INDEX IF NOT EXISTS idx_market_catalyst_source_claims_fingerprint ON market_catalyst_source_claims(claim_fingerprint)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_snapshot_catalysts_pair ON market_theme_snapshot_catalysts(snapshot_id, catalyst_event_id)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_snapshot_catalysts_event ON market_theme_snapshot_catalysts(catalyst_event_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_snapshot_constituents_public_id ON market_theme_snapshot_constituents(constituent_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_snapshot_constituents_asset ON market_theme_snapshot_constituents(snapshot_id, asset_type, market, code)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_snapshot_constituents_snapshot_rank ON market_theme_snapshot_constituents(snapshot_id, rank)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_evidence_links_public_id ON market_theme_evidence_links(link_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_market_theme_evidence_links_association ON market_theme_evidence_links(theme_id, COALESCE(snapshot_id, ''), COALESCE(catalyst_event_id, ''), COALESCE(source_claim_id, ''), evidence_item_id, link_type)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_evidence_links_theme_snapshot ON market_theme_evidence_links(theme_id, snapshot_id)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_evidence_links_catalyst ON market_theme_evidence_links(catalyst_event_id)`,
	`CREATE INDEX IF NOT EXISTS idx_market_theme_evidence_links_evidence ON market_theme_evidence_links(evidence_item_id)`,
}

var themeCatalystImmutabilityTriggerSQL = []string{
	`CREATE TRIGGER IF NOT EXISTS immutable_market_theme_daily_snapshots_update
BEFORE UPDATE ON market_theme_daily_snapshots
BEGIN
  SELECT RAISE(ABORT, 'market theme daily snapshot is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_market_theme_daily_snapshots_delete
BEFORE DELETE ON market_theme_daily_snapshots
BEGIN
  SELECT RAISE(ABORT, 'market theme daily snapshot is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_market_catalyst_source_claims_update
BEFORE UPDATE ON market_catalyst_source_claims
BEGIN
  SELECT RAISE(ABORT, 'market catalyst source claim is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_market_catalyst_source_claims_delete
BEFORE DELETE ON market_catalyst_source_claims
BEGIN
  SELECT RAISE(ABORT, 'market catalyst source claim is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_market_theme_snapshot_catalysts_update
BEFORE UPDATE ON market_theme_snapshot_catalysts
BEGIN
  SELECT RAISE(ABORT, 'market theme snapshot catalyst is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_market_theme_snapshot_catalysts_delete
BEFORE DELETE ON market_theme_snapshot_catalysts
BEGIN
  SELECT RAISE(ABORT, 'market theme snapshot catalyst is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_market_theme_snapshot_constituents_update
BEFORE UPDATE ON market_theme_snapshot_constituents
BEGIN
  SELECT RAISE(ABORT, 'market theme snapshot constituent is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS immutable_market_theme_snapshot_constituents_delete
BEFORE DELETE ON market_theme_snapshot_constituents
BEGIN
  SELECT RAISE(ABORT, 'market theme snapshot constituent is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS bounded_market_theme_snapshot_catalysts_insert
BEFORE INSERT ON market_theme_snapshot_catalysts
WHEN NOT EXISTS (
  SELECT 1 FROM market_theme_daily_snapshots snapshot
  WHERE snapshot.snapshot_id = NEW.snapshot_id
    AND (SELECT COUNT(*) FROM market_theme_snapshot_catalysts child WHERE child.snapshot_id = NEW.snapshot_id) < snapshot.catalyst_count
)
BEGIN
  SELECT RAISE(ABORT, 'market theme snapshot catalyst capacity exceeded');
END`,
	`CREATE TRIGGER IF NOT EXISTS bounded_market_theme_snapshot_constituents_insert
BEFORE INSERT ON market_theme_snapshot_constituents
WHEN NOT EXISTS (
  SELECT 1 FROM market_theme_daily_snapshots snapshot
  WHERE snapshot.snapshot_id = NEW.snapshot_id
    AND (SELECT COUNT(*) FROM market_theme_snapshot_constituents child WHERE child.snapshot_id = NEW.snapshot_id) < snapshot.constituent_count
)
BEGIN
  SELECT RAISE(ABORT, 'market theme snapshot constituent capacity exceeded');
END`,
}

func mainMigrationV17Definition() string {
	parts := append([]string{}, themeCatalystTableSQL...)
	parts = append(parts, themeCatalystIndexSQL...)
	parts = append(parts, themeCatalystImmutabilityTriggerSQL...)
	return strings.Join(parts, ";\n\n") + ";\n"
}

func applyThemeCatalystSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, tableSQL := range themeCatalystTableSQL {
		if err := tx.Exec(tableSQL).Error; err != nil {
			return fmt.Errorf("create theme catalyst table: %w", err)
		}
	}
	for _, indexSQL := range themeCatalystIndexSQL {
		if err := tx.Exec(indexSQL).Error; err != nil {
			return fmt.Errorf("create theme catalyst index: %w", err)
		}
	}
	for _, triggerSQL := range themeCatalystImmutabilityTriggerSQL {
		if err := tx.Exec(triggerSQL).Error; err != nil {
			return fmt.Errorf("create theme catalyst immutability trigger: %w", err)
		}
	}
	return verifyMainSchema17Runtime(tx)
}

func verifyMainSchema17Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	objects := make([]struct {
		objectType string
		name       string
		statement  string
	}, 0, len(themeCatalystTableSQL)+len(themeCatalystIndexSQL)+len(themeCatalystImmutabilityTriggerSQL))
	tableNames := []string{
		"market_themes",
		"market_theme_aliases",
		"market_theme_daily_snapshots",
		"market_catalyst_events",
		"market_catalyst_source_claims",
		"market_theme_snapshot_catalysts",
		"market_theme_snapshot_constituents",
		"market_theme_evidence_links",
	}
	for index, statement := range themeCatalystTableSQL {
		objects = append(objects, struct {
			objectType string
			name       string
			statement  string
		}{objectType: "table", name: tableNames[index], statement: statement})
	}
	for _, statement := range themeCatalystIndexSQL {
		objects = append(objects, struct {
			objectType string
			name       string
			statement  string
		}{objectType: "index", name: sqliteSchemaObjectName(statement), statement: sqliteStoredIndexSQL(statement)})
	}
	triggerNames := []string{
		"immutable_market_theme_daily_snapshots_update",
		"immutable_market_theme_daily_snapshots_delete",
		"immutable_market_catalyst_source_claims_update",
		"immutable_market_catalyst_source_claims_delete",
		"immutable_market_theme_snapshot_catalysts_update",
		"immutable_market_theme_snapshot_catalysts_delete",
		"immutable_market_theme_snapshot_constituents_update",
		"immutable_market_theme_snapshot_constituents_delete",
		"bounded_market_theme_snapshot_catalysts_insert",
		"bounded_market_theme_snapshot_constituents_insert",
	}
	for index, statement := range themeCatalystImmutabilityTriggerSQL {
		objects = append(objects, struct {
			objectType string
			name       string
			statement  string
		}{objectType: "trigger", name: triggerNames[index], statement: sqliteStoredIndexSQL(statement)})
	}
	for _, object := range objects {
		if err := verifySQLiteSchemaObject(database, object.objectType, object.name, object.statement); err != nil {
			return fmt.Errorf("verify main schema 17 %s %s: %w", object.objectType, object.name, err)
		}
	}
	return nil
}
