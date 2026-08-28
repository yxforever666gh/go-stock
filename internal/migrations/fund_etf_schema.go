package migrations

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const fundRankingSnapshotsTableSQL = `CREATE TABLE IF NOT EXISTS fund_ranking_snapshots (
  ranking_snapshot_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(ranking_snapshot_id)) > 0),
  source_name TEXT NOT NULL CHECK (length(trim(source_name)) > 0),
  source_ref TEXT,
  fund_code TEXT NOT NULL CHECK (length(trim(fund_code)) > 0),
  fund_name TEXT NOT NULL CHECK (length(trim(fund_name)) > 0),
  fund_category TEXT NOT NULL CHECK (fund_category IN ('stock', 'mixed', 'bond', 'index', 'qdii', 'fof')),
  ranking_period TEXT NOT NULL CHECK (ranking_period IN ('day', 'week', 'month', '3m', '6m', '1y', '3y', 'ytd', 'since_inception', 'scale')),
  trade_date TEXT NOT NULL CHECK (length(trim(trade_date)) = 10),
  nav_date TEXT CHECK (nav_date IS NULL OR length(trim(nav_date)) = 10),
  as_of DATETIME NOT NULL,
  fetched_at DATETIME NOT NULL,
  rank_position INTEGER NOT NULL CHECK (rank_position >= 1),
  rank_total INTEGER CHECK (rank_total IS NULL OR rank_total >= rank_position),
  unit_nav REAL CHECK (unit_nav IS NULL OR unit_nav >= 0),
  accumulated_nav REAL CHECK (accumulated_nav IS NULL OR accumulated_nav >= 0),
  return_1d_pct REAL,
  return_1w_pct REAL,
  return_1m_pct REAL,
  return_3m_pct REAL,
  return_6m_pct REAL,
  return_ytd_pct REAL,
  return_1y_pct REAL,
  return_3y_pct REAL,
  return_since_inception_pct REAL,
  fund_size_cny REAL CHECK (fund_size_cny IS NULL OR fund_size_cny >= 0),
  fund_size_as_of TEXT CHECK (fund_size_as_of IS NULL OR length(trim(fund_size_as_of)) = 10),
  subscription_status TEXT,
  redemption_status TEXT,
  raw_payload_sha256 CHAR(64) CHECK (raw_payload_sha256 IS NULL OR (length(raw_payload_sha256) = 64 AND raw_payload_sha256 NOT GLOB '*[^0-9A-Fa-f]*')),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const etfInstrumentsTableSQL = `CREATE TABLE IF NOT EXISTS etf_instruments (
  etf_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(etf_id)) > 0),
  market TEXT NOT NULL CHECK (market IN ('SH', 'SZ')),
  exchange TEXT NOT NULL CHECK (exchange IN ('SSE', 'SZSE')),
  code TEXT NOT NULL CHECK (length(trim(code)) > 0),
  exchange_symbol TEXT NOT NULL CHECK (length(trim(exchange_symbol)) > 0),
  fund_code TEXT,
  name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  category TEXT NOT NULL CHECK (category IN ('broad', 'industry', 'cross_border', 'bond', 'commodity', 'money')),
  tracking_index_code TEXT,
  tracking_index_name TEXT,
  management_fee_pct REAL CHECK (management_fee_pct IS NULL OR management_fee_pct >= 0),
  currency TEXT NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
  listed_at TEXT CHECK (listed_at IS NULL OR length(trim(listed_at)) = 10),
  delisted_at TEXT CHECK (delisted_at IS NULL OR length(trim(delisted_at)) = 10),
  status TEXT NOT NULL CHECK (status IN ('listed', 'suspended', 'delisted')),
  source_name TEXT NOT NULL CHECK (length(trim(source_name)) > 0),
  source_ref TEXT,
  as_of DATETIME NOT NULL,
  fetched_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  CHECK ((market = 'SH' AND exchange = 'SSE') OR (market = 'SZ' AND exchange = 'SZSE'))
)`

const etfMarketSnapshotsTableSQL = `CREATE TABLE IF NOT EXISTS etf_market_snapshots (
  market_snapshot_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(market_snapshot_id)) > 0),
  etf_id TEXT NOT NULL CHECK (length(trim(etf_id)) > 0),
  source_name TEXT NOT NULL CHECK (length(trim(source_name)) > 0),
  source_ref TEXT,
  trade_date TEXT NOT NULL CHECK (length(trim(trade_date)) = 10),
  as_of DATETIME NOT NULL,
  fetched_at DATETIME NOT NULL,
  open_price REAL CHECK (open_price IS NULL OR open_price >= 0),
  high_price REAL CHECK (high_price IS NULL OR high_price >= 0),
  low_price REAL CHECK (low_price IS NULL OR low_price >= 0),
  close_price REAL CHECK (close_price IS NULL OR close_price >= 0),
  previous_close REAL CHECK (previous_close IS NULL OR previous_close >= 0),
  change_pct REAL,
  volume REAL CHECK (volume IS NULL OR volume >= 0),
  turnover_amount REAL CHECK (turnover_amount IS NULL OR turnover_amount >= 0),
  turnover_rate_pct REAL CHECK (turnover_rate_pct IS NULL OR turnover_rate_pct >= 0),
  raw_payload_sha256 CHAR(64) CHECK (raw_payload_sha256 IS NULL OR (length(raw_payload_sha256) = 64 AND raw_payload_sha256 NOT GLOB '*[^0-9A-Fa-f]*')),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const etfNAVSnapshotsTableSQL = `CREATE TABLE IF NOT EXISTS etf_nav_snapshots (
  nav_snapshot_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(nav_snapshot_id)) > 0),
  etf_id TEXT NOT NULL CHECK (length(trim(etf_id)) > 0),
  source_name TEXT NOT NULL CHECK (length(trim(source_name)) > 0),
  source_ref TEXT,
  nav_date TEXT NOT NULL CHECK (length(trim(nav_date)) = 10),
  trade_date TEXT CHECK (trade_date IS NULL OR length(trim(trade_date)) = 10),
  as_of DATETIME NOT NULL,
  fetched_at DATETIME NOT NULL,
  unit_nav REAL CHECK (unit_nav IS NULL OR unit_nav >= 0),
  accumulated_nav REAL CHECK (accumulated_nav IS NULL OR accumulated_nav >= 0),
  iopv REAL CHECK (iopv IS NULL OR iopv >= 0),
  market_price REAL CHECK (market_price IS NULL OR market_price >= 0),
  premium_discount_pct REAL,
  fund_size_cny REAL CHECK (fund_size_cny IS NULL OR fund_size_cny >= 0),
  fund_size_as_of TEXT CHECK (fund_size_as_of IS NULL OR length(trim(fund_size_as_of)) = 10),
  shares_outstanding REAL CHECK (shares_outstanding IS NULL OR shares_outstanding >= 0),
  raw_payload_sha256 CHAR(64) CHECK (raw_payload_sha256 IS NULL OR (length(raw_payload_sha256) = 64 AND raw_payload_sha256 NOT GLOB '*[^0-9A-Fa-f]*')),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const etfFundFlowSnapshotsTableSQL = `CREATE TABLE IF NOT EXISTS etf_fund_flow_snapshots (
  flow_snapshot_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(flow_snapshot_id)) > 0),
  etf_id TEXT NOT NULL CHECK (length(trim(etf_id)) > 0),
  source_name TEXT NOT NULL CHECK (length(trim(source_name)) > 0),
  source_ref TEXT,
  trade_date TEXT NOT NULL CHECK (length(trim(trade_date)) = 10),
  as_of DATETIME NOT NULL,
  fetched_at DATETIME NOT NULL,
  shares_outstanding REAL CHECK (shares_outstanding IS NULL OR shares_outstanding >= 0),
  share_change REAL,
  net_subscription_shares REAL,
  net_flow_cny REAL,
  net_flow_1d_cny REAL,
  net_flow_5d_cny REAL,
  net_flow_20d_cny REAL,
  fund_size_cny REAL CHECK (fund_size_cny IS NULL OR fund_size_cny >= 0),
  turnover_amount REAL CHECK (turnover_amount IS NULL OR turnover_amount >= 0),
  raw_payload_sha256 CHAR(64) CHECK (raw_payload_sha256 IS NULL OR (length(raw_payload_sha256) = 64 AND raw_payload_sha256 NOT GLOB '*[^0-9A-Fa-f]*')),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const etfHoldingSnapshotsTableSQL = `CREATE TABLE IF NOT EXISTS etf_holding_snapshots (
  holding_snapshot_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(holding_snapshot_id)) > 0),
  etf_id TEXT NOT NULL CHECK (length(trim(etf_id)) > 0),
  source_name TEXT NOT NULL CHECK (length(trim(source_name)) > 0),
  source_ref TEXT,
  report_date TEXT NOT NULL CHECK (length(trim(report_date)) = 10),
  as_of DATETIME NOT NULL,
  fetched_at DATETIME NOT NULL,
  total_positions INTEGER CHECK (total_positions IS NULL OR total_positions >= 0),
  disclosed_asset_ratio_pct REAL CHECK (disclosed_asset_ratio_pct IS NULL OR (disclosed_asset_ratio_pct >= 0 AND disclosed_asset_ratio_pct <= 100)),
  raw_payload_sha256 CHAR(64) CHECK (raw_payload_sha256 IS NULL OR (length(raw_payload_sha256) = 64 AND raw_payload_sha256 NOT GLOB '*[^0-9A-Fa-f]*')),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const etfHoldingPositionsTableSQL = `CREATE TABLE IF NOT EXISTS etf_holding_positions (
  holding_position_id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(holding_position_id)) > 0),
  holding_snapshot_id TEXT NOT NULL CHECK (length(trim(holding_snapshot_id)) > 0),
  rank INTEGER NOT NULL CHECK (rank >= 1),
  asset_type TEXT NOT NULL CHECK (asset_type IN ('stock', 'bond', 'fund', 'cash', 'other')),
  market TEXT,
  code TEXT NOT NULL CHECK (length(trim(code)) > 0),
  name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  quantity REAL CHECK (quantity IS NULL OR quantity >= 0),
  market_value REAL CHECK (market_value IS NULL OR market_value >= 0),
  weight_pct REAL CHECK (weight_pct IS NULL OR (weight_pct >= 0 AND weight_pct <= 100)),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

const etfWatchlistTableSQL = `CREATE TABLE IF NOT EXISTS etf_watchlist (
  code TEXT NOT NULL PRIMARY KEY CHECK (length(trim(code)) > 0),
  name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  market TEXT NOT NULL CHECK (market IN ('SH', 'SZ')),
  category TEXT NOT NULL CHECK (category IN ('broad', 'industry', 'cross_border', 'bond', 'commodity', 'money')),
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
)`

var fundETFTableSQL = []string{
	fundRankingSnapshotsTableSQL,
	etfInstrumentsTableSQL,
	etfMarketSnapshotsTableSQL,
	etfNAVSnapshotsTableSQL,
	etfFundFlowSnapshotsTableSQL,
	etfHoldingSnapshotsTableSQL,
	etfHoldingPositionsTableSQL,
	etfWatchlistTableSQL,
}

var fundETFIndexSQL = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_fund_ranking_source_code_category_period_date ON fund_ranking_snapshots(source_name, fund_code, fund_category, ranking_period, trade_date)`,
	`CREATE INDEX IF NOT EXISTS idx_fund_ranking_category_period_date_rank ON fund_ranking_snapshots(fund_category, ranking_period, trade_date, rank_position)`,
	`CREATE INDEX IF NOT EXISTS idx_fund_ranking_fund_date ON fund_ranking_snapshots(fund_code, trade_date)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_instruments_market_code ON etf_instruments(market, code)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_instruments_exchange_symbol ON etf_instruments(exchange, exchange_symbol)`,
	`CREATE INDEX IF NOT EXISTS idx_etf_instruments_category_status ON etf_instruments(category, status)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_market_source_etf_date ON etf_market_snapshots(source_name, etf_id, trade_date)`,
	`CREATE INDEX IF NOT EXISTS idx_etf_market_date_change ON etf_market_snapshots(trade_date, change_pct)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_nav_source_etf_date ON etf_nav_snapshots(source_name, etf_id, nav_date)`,
	`CREATE INDEX IF NOT EXISTS idx_etf_nav_date_premium ON etf_nav_snapshots(nav_date, premium_discount_pct)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_flow_source_etf_date ON etf_fund_flow_snapshots(source_name, etf_id, trade_date)`,
	`CREATE INDEX IF NOT EXISTS idx_etf_flow_date_net_flow ON etf_fund_flow_snapshots(trade_date, net_flow_cny)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_holding_source_etf_report ON etf_holding_snapshots(source_name, etf_id, report_date)`,
	`CREATE INDEX IF NOT EXISTS idx_etf_holding_etf_report ON etf_holding_snapshots(etf_id, report_date)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_holding_position_rank ON etf_holding_positions(holding_snapshot_id, rank)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_holding_position_asset ON etf_holding_positions(holding_snapshot_id, asset_type, COALESCE(market, ''), code)`,
	`CREATE INDEX IF NOT EXISTS idx_etf_holding_position_snapshot_weight ON etf_holding_positions(holding_snapshot_id, weight_pct)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_etf_watchlist_code ON etf_watchlist(code)`,
	`CREATE INDEX IF NOT EXISTS idx_etf_watchlist_market_category ON etf_watchlist(market, category)`,
}

var fundETFIdentityTriggerSQL = []string{
	`CREATE TRIGGER IF NOT EXISTS identity_fund_ranking_snapshots_update
BEFORE UPDATE ON fund_ranking_snapshots
WHEN NEW.ranking_snapshot_id IS NOT OLD.ranking_snapshot_id
  OR NEW.source_name IS NOT OLD.source_name
  OR NEW.fund_code IS NOT OLD.fund_code
  OR NEW.fund_category IS NOT OLD.fund_category
  OR NEW.ranking_period IS NOT OLD.ranking_period
  OR NEW.trade_date IS NOT OLD.trade_date
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'fund ranking snapshot identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_etf_instruments_update
BEFORE UPDATE ON etf_instruments
WHEN NEW.etf_id IS NOT OLD.etf_id
  OR NEW.market IS NOT OLD.market
  OR NEW.exchange IS NOT OLD.exchange
  OR NEW.code IS NOT OLD.code
  OR NEW.exchange_symbol IS NOT OLD.exchange_symbol
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'ETF instrument identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_etf_market_snapshots_update
BEFORE UPDATE ON etf_market_snapshots
WHEN NEW.market_snapshot_id IS NOT OLD.market_snapshot_id
  OR NEW.etf_id IS NOT OLD.etf_id
  OR NEW.source_name IS NOT OLD.source_name
  OR NEW.trade_date IS NOT OLD.trade_date
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'ETF market snapshot identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_etf_nav_snapshots_update
BEFORE UPDATE ON etf_nav_snapshots
WHEN NEW.nav_snapshot_id IS NOT OLD.nav_snapshot_id
  OR NEW.etf_id IS NOT OLD.etf_id
  OR NEW.source_name IS NOT OLD.source_name
  OR NEW.nav_date IS NOT OLD.nav_date
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'ETF NAV snapshot identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_etf_fund_flow_snapshots_update
BEFORE UPDATE ON etf_fund_flow_snapshots
WHEN NEW.flow_snapshot_id IS NOT OLD.flow_snapshot_id
  OR NEW.etf_id IS NOT OLD.etf_id
  OR NEW.source_name IS NOT OLD.source_name
  OR NEW.trade_date IS NOT OLD.trade_date
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'ETF fund-flow snapshot identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_etf_holding_snapshots_update
BEFORE UPDATE ON etf_holding_snapshots
WHEN NEW.holding_snapshot_id IS NOT OLD.holding_snapshot_id
  OR NEW.etf_id IS NOT OLD.etf_id
  OR NEW.source_name IS NOT OLD.source_name
  OR NEW.report_date IS NOT OLD.report_date
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'ETF holding snapshot identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_etf_holding_positions_update
BEFORE UPDATE ON etf_holding_positions
WHEN NEW.holding_position_id IS NOT OLD.holding_position_id
  OR NEW.holding_snapshot_id IS NOT OLD.holding_snapshot_id
  OR NEW.rank IS NOT OLD.rank
  OR NEW.asset_type IS NOT OLD.asset_type
  OR NEW.market IS NOT OLD.market
  OR NEW.code IS NOT OLD.code
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'ETF holding position identity is immutable');
END`,
	`CREATE TRIGGER IF NOT EXISTS identity_etf_watchlist_update
BEFORE UPDATE ON etf_watchlist
WHEN NEW.code IS NOT OLD.code
  OR NEW.created_at IS NOT OLD.created_at
BEGIN
  SELECT RAISE(ABORT, 'ETF watchlist identity is immutable');
END`,
}

func mainMigrationV20Definition() string {
	parts := append([]string{}, fundETFTableSQL...)
	parts = append(parts, fundETFIndexSQL...)
	parts = append(parts, fundETFIdentityTriggerSQL...)
	return strings.Join(parts, ";\n\n") + ";\n"
}

func applyFundETFSchema(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("main database is unavailable")
	}
	for _, statement := range fundETFTableSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create fund/ETF table: %w", err)
		}
	}
	for _, statement := range fundETFIndexSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create fund/ETF index: %w", err)
		}
	}
	for _, statement := range fundETFIdentityTriggerSQL {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("create fund/ETF identity trigger: %w", err)
		}
	}
	return verifyMainSchema20Runtime(tx)
}

func verifyMainSchema20Runtime(database *gorm.DB) error {
	if database == nil {
		return errors.New("main database is unavailable")
	}
	tableNames := []string{
		"fund_ranking_snapshots",
		"etf_instruments",
		"etf_market_snapshots",
		"etf_nav_snapshots",
		"etf_fund_flow_snapshots",
		"etf_holding_snapshots",
		"etf_holding_positions",
		"etf_watchlist",
	}
	for index, statement := range fundETFTableSQL {
		if err := verifySQLiteSchemaObject(database, "table", tableNames[index], statement); err != nil {
			return fmt.Errorf("verify main schema 20 table %s: %w", tableNames[index], err)
		}
	}
	for _, statement := range fundETFIndexSQL {
		name := sqliteSchemaObjectName(statement)
		if err := verifySQLiteSchemaObject(database, "index", name, sqliteStoredIndexSQL(statement)); err != nil {
			return fmt.Errorf("verify main schema 20 index %s: %w", name, err)
		}
	}
	triggerNames := []string{
		"identity_fund_ranking_snapshots_update",
		"identity_etf_instruments_update",
		"identity_etf_market_snapshots_update",
		"identity_etf_nav_snapshots_update",
		"identity_etf_fund_flow_snapshots_update",
		"identity_etf_holding_snapshots_update",
		"identity_etf_holding_positions_update",
		"identity_etf_watchlist_update",
	}
	for index, statement := range fundETFIdentityTriggerSQL {
		name := triggerNames[index]
		if err := verifySQLiteSchemaObject(database, "trigger", name, sqliteStoredIndexSQL(statement)); err != nil {
			return fmt.Errorf("verify main schema 20 trigger %s: %w", name, err)
		}
	}
	return nil
}
