package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// dailySchemaSQL 是自建 schema。与 auction.go 一样由本进程负责创建，
// 不经过主程序的 migrations 引擎，因此不需要提升 mainSchemaVersion。
const dailySchemaSQL = `PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS daily_bar (
  stock_code TEXT NOT NULL,
  trade_date TEXT NOT NULL,
  stock_name TEXT,
  open REAL, high REAL, low REAL, close REAL,
  pre_close REAL, pct_chg REAL,
  volume REAL, amount REAL,
  PRIMARY KEY (stock_code, trade_date)
) WITHOUT ROWID;
-- (trade_date, pct_chg) 是覆盖索引：涨跌家数聚合只需这两列，可完全在索引内完成。
-- 单列 (trade_date) 索引会退回主表做逐行查找，实测 4.8M 行时慢 60 倍。
DROP INDEX IF EXISTS idx_daily_bar_date;
CREATE INDEX IF NOT EXISTS idx_daily_bar_date_pct ON daily_bar(trade_date, pct_chg);
CREATE TABLE IF NOT EXISTS refresh_log (
  dataset TEXT NOT NULL,
  trade_date TEXT NOT NULL,
  status TEXT NOT NULL,
  rows INTEGER NOT NULL,
  detail TEXT,
  fetched_at TEXT NOT NULL,
  PRIMARY KEY (dataset, trade_date)
) WITHOUT ROWID;`

const dailyDataset = "daily_bar"

type dailyStore struct {
	db   *sql.DB
	file string
}

func (s *dailyStore) path() string {
	if s == nil {
		return ""
	}
	return s.file
}

// openDailyStore 打开 <dir>/daily.sqlite。writable=false 时走只读 DSN；
// 文件不存在会先建好空库再以只读打开，保证服务端在首次刷新前也能正常启动。
func openDailyStore(dir string, writable bool) (*dailyStore, error) {
	if writable {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	path, err := filepath.Abs(filepath.Join(dir, "daily.sqlite"))
	if err != nil {
		return nil, err
	}
	if !writable {
		if _, err := os.Stat(path); err != nil {
			bootstrap, err := mkDailyStore(path)
			if err != nil {
				return nil, err
			}
			bootstrap.close()
		}
		return mkReadOnlyDailyStore(path)
	}
	return mkDailyStore(path)
}

func mkDailyStore(path string) (*dailyStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &dailyStore{db: db, file: path}
	if _, err := db.Exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=10000;"); err != nil {
		return nil, s.fail(err)
	}
	if _, err := db.Exec(dailySchemaSQL); err != nil {
		return nil, s.fail(err)
	}
	return s, nil
}

func mkReadOnlyDailyStore(path string) (*dailyStore, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &dailyStore{db: db, file: path}
	if _, err := db.Exec("PRAGMA busy_timeout=10000;"); err != nil {
		return nil, s.fail(err)
	}
	return s, nil
}

func (s *dailyStore) fail(err error) error {
	s.db.Close()
	return err
}

func (s *dailyStore) close() {
	if s != nil && s.db != nil {
		s.db.Close()
	}
}

type dailyBarRow struct {
	Code      string
	Date      string
	Name      string
	Open      float64
	High      float64
	Low       float64
	Close     float64
	PreClose  float64
	PctChg    float64
	Volume    float64
	Amount    float64
}

// putBars 在一个事务里替换某交易日的全部行：先删该日再批量插入，
// 使重复刷新同一日期是幂等的，不会留下半新半旧的数据。
func (s *dailyStore) putBars(date string, rows []dailyBarRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM daily_bar WHERE trade_date=?", date); err != nil {
		tx.Rollback()
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO daily_bar
(stock_code,trade_date,stock_name,open,high,low,close,pre_close,pct_chg,volume,amount)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(r.Code, r.Date, r.Name, r.Open, r.High, r.Low, r.Close, r.PreClose, r.PctChg, r.Volume, r.Amount); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *dailyStore) markRefreshed(date, status string, count int, detail string, at string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO refresh_log
(dataset,trade_date,status,rows,detail,fetched_at) VALUES (?,?,?,?,?,?)`,
		dailyDataset, date, status, count, detail, at)
	return err
}

func (s *dailyStore) refreshedDates() (map[string]string, error) {
	rows, err := s.db.Query("SELECT trade_date,status FROM refresh_log WHERE dataset=?", dailyDataset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var date, status string
		if err := rows.Scan(&date, &status); err != nil {
			return nil, err
		}
		out[date] = status
	}
	return out, rows.Err()
}

type breadthRow struct {
	Date  string `json:"trade_date"`
	Up    int    `json:"up"`
	Down  int    `json:"down"`
	Flat  int    `json:"flat"`
	Total int    `json:"total"`
}

// breadthQuery 构造横截面聚合语句。抽出来是为了让测试能对真实语句做 EXPLAIN，
// 保证「必须走覆盖索引」这个性能特征不会随 SQL 改动悄悄失效。
func breadthQuery(start, end string) (string, []any) {
	q := `SELECT trade_date,
       SUM(CASE WHEN pct_chg > 0 THEN 1 ELSE 0 END),
       SUM(CASE WHEN pct_chg < 0 THEN 1 ELSE 0 END),
       SUM(CASE WHEN pct_chg = 0 THEN 1 ELSE 0 END),
       COUNT(*)
FROM daily_bar
WHERE pct_chg IS NOT NULL`
	args := []any{}
	if start != "" {
		q += " AND trade_date >= ?"
		args = append(args, start)
	}
	if end != "" {
		q += " AND trade_date <= ?"
		args = append(args, end)
	}
	q += " GROUP BY trade_date ORDER BY trade_date"
	return q, args
}

// breadth 是横截面聚合：走覆盖索引 idx_daily_bar_date_pct，一次 GROUP BY 出每日涨跌家数。
func (s *dailyStore) breadth(start, end string) ([]breadthRow, error) {
	q, args := breadthQuery(start, end)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []breadthRow{}
	for rows.Next() {
		var r breadthRow
		if err := rows.Scan(&r.Date, &r.Up, &r.Down, &r.Flat, &r.Total); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// bars 走主键 (stock_code,trade_date) 前缀 seek，服务时序查询。
func (s *dailyStore) bars(codes []string, start, end string) ([]dailyBarRow, error) {
	if len(codes) == 0 {
		return nil, fmt.Errorf("至少需要一个股票代码")
	}
	args := make([]any, 0, len(codes)+2)
	placeholders := make([]string, len(codes))
	for i, c := range codes {
		placeholders[i] = "?"
		args = append(args, c)
	}
	q := `SELECT stock_code,trade_date,COALESCE(stock_name,''),open,high,low,close,
       COALESCE(pre_close,0),COALESCE(pct_chg,0),COALESCE(volume,0),COALESCE(amount,0)
FROM daily_bar WHERE stock_code IN (` + strings.Join(placeholders, ",") + ")"
	if start != "" {
		q += " AND trade_date >= ?"
		args = append(args, start)
	}
	if end != "" {
		q += " AND trade_date <= ?"
		args = append(args, end)
	}
	q += " ORDER BY stock_code, trade_date"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dailyBarRow{}
	for rows.Next() {
		var r dailyBarRow
		if err := rows.Scan(&r.Code, &r.Date, &r.Name, &r.Open, &r.High, &r.Low, &r.Close, &r.PreClose, &r.PctChg, &r.Volume, &r.Amount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type refreshStatus struct {
	FirstDate    string   `json:"first_date"`
	LastDate     string   `json:"last_date"`
	Bars         int      `json:"bars"`
	Dates        int      `json:"dates"`
	FailedDates  []string `json:"failed_dates"`
	FetchedAt    string   `json:"last_fetched_at"`
}

func (s *dailyStore) status() (refreshStatus, error) {
	var out refreshStatus
	out.FailedDates = []string{}
	row := s.db.QueryRow(`SELECT COALESCE(MIN(trade_date),''),COALESCE(MAX(trade_date),''),COUNT(*),COUNT(DISTINCT trade_date) FROM daily_bar`)
	if err := row.Scan(&out.FirstDate, &out.LastDate, &out.Bars, &out.Dates); err != nil {
		return out, err
	}
	rows, err := s.db.Query(`SELECT trade_date FROM refresh_log WHERE dataset=? AND status<>'ok' ORDER BY trade_date`, dailyDataset)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return out, err
		}
		out.FailedDates = append(out.FailedDates, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	var at sql.NullString
	if err := s.db.QueryRow(`SELECT MAX(fetched_at) FROM refresh_log WHERE dataset=?`, dailyDataset).Scan(&at); err != nil && err != sql.ErrNoRows {
		return out, err
	}
	out.FetchedAt = at.String
	return out, nil
}
