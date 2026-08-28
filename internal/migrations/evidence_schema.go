package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const researchEvidenceSetTableSQL = `CREATE TABLE IF NOT EXISTS research_evidence_sets (
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
)`

const researchEvidenceItemTableSQL = `CREATE TABLE IF NOT EXISTS research_evidence_items (
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
)`

var researchEvidenceIndexSQL = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_research_evidence_sets_public_id ON research_evidence_sets(evidence_set_id)`,
	`CREATE INDEX IF NOT EXISTS idx_research_evidence_sets_owner ON research_evidence_sets(owner_type, owner_id)`,
	`CREATE INDEX IF NOT EXISTS idx_research_evidence_sets_cutoff ON research_evidence_sets(cutoff_at)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_research_evidence_items_public_id ON research_evidence_items(evidence_item_id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_research_evidence_items_set_source ON research_evidence_items(evidence_set_id, source_id)`,
	`CREATE INDEX IF NOT EXISTS idx_research_evidence_items_category ON research_evidence_items(category)`,
	`CREATE INDEX IF NOT EXISTS idx_research_evidence_items_entity ON research_evidence_items(entity_type, entity_id)`,
	`CREATE INDEX IF NOT EXISTS idx_research_evidence_items_available ON research_evidence_items(evidence_set_id, available_at)`,
	`CREATE INDEX IF NOT EXISTS idx_research_evidence_items_collected ON research_evidence_items(collected_at)`,
}

const marketBarCacheTableSQL = `CREATE TABLE IF NOT EXISTS market_bar_cache (
  asset_type TEXT NOT NULL,
  symbol TEXT NOT NULL,
  period TEXT NOT NULL,
  adjustment TEXT NOT NULL,
  bar_time INTEGER NOT NULL,
  open REAL NOT NULL,
  high REAL NOT NULL,
  low REAL NOT NULL,
  close REAL NOT NULL,
  volume REAL,
  amount REAL,
  source TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (asset_type, symbol, period, adjustment, bar_time)
) WITHOUT ROWID`

const marketAuctionSnapshotTableSQL = `CREATE TABLE IF NOT EXISTS market_auction_snapshot (
  asset_type TEXT NOT NULL,
  symbol TEXT NOT NULL,
  trade_date TEXT NOT NULL,
  observed_at INTEGER NOT NULL,
  phase TEXT NOT NULL,
  indicative_price REAL,
  matched_volume REAL,
  matched_amount REAL,
  unmatched_volume REAL,
  unmatched_side TEXT,
  source TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (asset_type, symbol, trade_date, observed_at, phase)
) WITHOUT ROWID`

const marketTradeTickTableSQL = `CREATE TABLE IF NOT EXISTS market_trade_tick (
  asset_type TEXT NOT NULL,
  symbol TEXT NOT NULL,
  traded_at INTEGER NOT NULL,
  sequence INTEGER NOT NULL,
  price REAL NOT NULL,
  volume REAL NOT NULL,
  amount REAL,
  side TEXT,
  source TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (asset_type, symbol, traded_at, sequence)
) WITHOUT ROWID`

var marketEvidenceCacheIndexSQL = []string{
	`CREATE INDEX IF NOT EXISTS idx_market_bar_cache_time ON market_bar_cache(bar_time)`,
	`CREATE INDEX IF NOT EXISTS idx_market_auction_snapshot_date ON market_auction_snapshot(trade_date)`,
	`CREATE INDEX IF NOT EXISTS idx_market_auction_snapshot_observed ON market_auction_snapshot(observed_at)`,
	`CREATE INDEX IF NOT EXISTS idx_market_trade_tick_time ON market_trade_tick(traded_at)`,
}

func mainMigrationV15Definition() string {
	parts := []string{
		researchEvidenceSetTableSQL,
		researchEvidenceItemTableSQL,
		"ALTER TABLE settings ADD COLUMN experimental_evidence_enabled NUMERIC NOT NULL DEFAULT 0",
		"ALTER TABLE research_v160_analysis_runs ADD COLUMN strategy_version TEXT",
		"ALTER TABLE research_v160_analysis_runs ADD COLUMN evidence_profile_version TEXT",
		"ALTER TABLE research_v160_analysis_runs ADD COLUMN evidence_set_id TEXT",
		"ALTER TABLE research2_analysis_runs ADD COLUMN strategy_version TEXT",
		"ALTER TABLE research2_analysis_runs ADD COLUMN evidence_profile_version TEXT",
		"ALTER TABLE research2_analysis_runs ADD COLUMN evidence_set_id TEXT",
	}
	parts = append(parts, researchEvidenceIndexSQL...)
	return strings.Join(parts, ";\n\n")
}

func minuteMigrationV3Definition() string {
	parts := []string{marketBarCacheTableSQL, marketAuctionSnapshotTableSQL, marketTradeTickTableSQL}
	parts = append(parts, marketEvidenceCacheIndexSQL...)
	return strings.Join(parts, ";\n\n") + ";\n"
}

func applyResearchEvidenceSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, tableSQL := range []string{researchEvidenceSetTableSQL, researchEvidenceItemTableSQL} {
		if err := tx.Exec(tableSQL).Error; err != nil {
			return fmt.Errorf("create research evidence table: %w", err)
		}
	}
	for _, indexSQL := range researchEvidenceIndexSQL {
		if err := tx.Exec(indexSQL).Error; err != nil {
			return fmt.Errorf("create research evidence index: %w", err)
		}
	}
	columns := []struct {
		table      string
		column     string
		definition string
	}{
		{table: "settings", column: "experimental_evidence_enabled", definition: "NUMERIC NOT NULL DEFAULT 0"},
		{table: "research_v160_analysis_runs", column: "strategy_version", definition: "TEXT"},
		{table: "research_v160_analysis_runs", column: "evidence_profile_version", definition: "TEXT"},
		{table: "research_v160_analysis_runs", column: "evidence_set_id", definition: "TEXT"},
		{table: "research2_analysis_runs", column: "strategy_version", definition: "TEXT"},
		{table: "research2_analysis_runs", column: "evidence_profile_version", definition: "TEXT"},
		{table: "research2_analysis_runs", column: "evidence_set_id", definition: "TEXT"},
	}
	for _, column := range columns {
		if tx.Migrator().HasColumn(column.table, column.column) {
			continue
		}
		statement := "ALTER TABLE " + quoteSQLiteIdentifier(column.table) + " ADD COLUMN " + quoteSQLiteIdentifier(column.column) + " " + column.definition
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("add %s.%s: %w", column.table, column.column, err)
		}
	}
	return verifyMainSchema15Runtime(tx)
}

func applyMarketEvidenceCacheSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("minute database is unavailable")
	}
	for _, tableSQL := range []string{marketBarCacheTableSQL, marketAuctionSnapshotTableSQL, marketTradeTickTableSQL} {
		if err := tx.Exec(tableSQL).Error; err != nil {
			return fmt.Errorf("create market evidence cache table: %w", err)
		}
	}
	for _, indexSQL := range marketEvidenceCacheIndexSQL {
		if err := tx.Exec(indexSQL).Error; err != nil {
			return fmt.Errorf("create market evidence cache index: %w", err)
		}
	}
	return verifyMinuteSchema3Runtime(tx)
}

func verifyMainSchema15Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	objects := []struct {
		objectType string
		name       string
		statement  string
	}{
		{objectType: "table", name: "research_evidence_sets", statement: researchEvidenceSetTableSQL},
		{objectType: "table", name: "research_evidence_items", statement: researchEvidenceItemTableSQL},
	}
	for _, statement := range researchEvidenceIndexSQL {
		objects = append(objects, struct {
			objectType string
			name       string
			statement  string
		}{objectType: "index", name: sqliteSchemaObjectName(statement), statement: sqliteStoredIndexSQL(statement)})
	}
	for _, object := range objects {
		if err := verifySQLiteSchemaObject(database, object.objectType, object.name, object.statement); err != nil {
			return fmt.Errorf("verify main schema 15 %s %s: %w", object.objectType, object.name, err)
		}
	}
	columns := map[string][]string{
		"settings":                    {"experimental_evidence_enabled"},
		"research_v160_analysis_runs": {"strategy_version", "evidence_profile_version", "evidence_set_id"},
		"research2_analysis_runs":     {"strategy_version", "evidence_profile_version", "evidence_set_id"},
	}
	for table, names := range columns {
		for _, name := range names {
			if !database.Migrator().HasColumn(table, name) {
				return fmt.Errorf("main schema 15 missing %s.%s", table, name)
			}
		}
	}
	var invalidSettings int64
	if err := database.Raw("SELECT COUNT(*) FROM settings WHERE experimental_evidence_enabled IS NULL").Scan(&invalidSettings).Error; err != nil {
		return err
	}
	if invalidSettings != 0 {
		return fmt.Errorf("main schema 15 has %d settings rows without experimental evidence state", invalidSettings)
	}
	return nil
}

func verifyMinuteSchema3Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("minute database is unavailable")
	}
	objects := []struct {
		objectType string
		name       string
		statement  string
	}{
		{objectType: "table", name: "market_bar_cache", statement: marketBarCacheTableSQL},
		{objectType: "table", name: "market_auction_snapshot", statement: marketAuctionSnapshotTableSQL},
		{objectType: "table", name: "market_trade_tick", statement: marketTradeTickTableSQL},
	}
	for _, statement := range marketEvidenceCacheIndexSQL {
		objects = append(objects, struct {
			objectType string
			name       string
			statement  string
		}{objectType: "index", name: sqliteSchemaObjectName(statement), statement: sqliteStoredIndexSQL(statement)})
	}
	for _, object := range objects {
		if err := verifySQLiteSchemaObject(database, object.objectType, object.name, object.statement); err != nil {
			return fmt.Errorf("verify minute schema 3 %s %s: %w", object.objectType, object.name, err)
		}
	}
	return nil
}

func sqliteSchemaObjectName(statement string) string {
	fields := strings.Fields(statement)
	for index, field := range fields {
		if strings.EqualFold(field, "INDEX") && index+1 < len(fields) {
			nameIndex := index + 1
			if strings.EqualFold(fields[nameIndex], "IF") && nameIndex+3 < len(fields) {
				nameIndex += 3
			}
			return strings.Trim(fields[nameIndex], "`\"")
		}
	}
	return ""
}

func sqliteStoredIndexSQL(statement string) string {
	return strings.Replace(statement, " IF NOT EXISTS", "", 1)
}
