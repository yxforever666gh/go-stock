package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var auctionHeaders = []string{"code", "trade_date", "prev_close", "current", "volume", "amount", "cjcs", "b1_p", "b1_v", "b2_p", "b2_v", "a1_p", "a1_v", "a2_p", "a2_v", "total_bid", "average_bid", "total_ask", "average_ask"}

type auctionIndex struct {
	db    *sql.DB
	root  string
	files []sourceFile
}

func openAuctionIndex(root, dir string, prepare bool) (*auctionIndex, error) {
	if prepare {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	path, err := filepath.Abs(filepath.Join(dir, "positions.sqlite"))
	if err != nil {
		return nil, err
	}
	dsn := path
	if !prepare {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("请先运行 -prepare-data 建立竞价索引")
		}
		dsn = "file:" + filepath.ToSlash(path) + "?mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*auctionIndex, error) { db.Close(); return nil, err }
	if _, err := db.Exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		return fail(err)
	}
	if prepare {
		_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS files (id INTEGER PRIMARY KEY, rel TEXT NOT NULL UNIQUE, size INTEGER NOT NULL, modified INTEGER NOT NULL, records INTEGER NOT NULL, first_time TEXT NOT NULL, last_time TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS blocks (file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE, symbol TEXT NOT NULL, day TEXT NOT NULL, start INTEGER NOT NULL, end INTEGER NOT NULL, PRIMARY KEY(file_id,start)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS blocks_lookup ON blocks(symbol,day);`)
		if err != nil {
			return fail(err)
		}
		var savedRoot string
		err = db.QueryRow("SELECT value FROM metadata WHERE key='root'").Scan(&savedRoot)
		if err != nil && err != sql.ErrNoRows {
			return fail(err)
		}
		if savedRoot != root {
			tx, err := db.Begin()
			if err != nil {
				return fail(err)
			}
			if _, err = tx.Exec("DELETE FROM files"); err == nil {
				_, err = tx.Exec("INSERT OR REPLACE INTO metadata(key,value) VALUES('root',?)", root)
			}
			if err != nil {
				tx.Rollback()
				return fail(err)
			}
			if err = tx.Commit(); err != nil {
				return fail(err)
			}
		}
	} else {
		var savedRoot string
		if err := db.QueryRow("SELECT value FROM metadata WHERE key='root'").Scan(&savedRoot); err != nil || savedRoot != root {
			return fail(fmt.Errorf("索引不属于当前 data-root，请重新 -prepare-data"))
		}
	}
	return &auctionIndex{db: db, root: root}, nil
}

func (a *auctionIndex) close() { a.db.Close() }

func sameSource(file sourceFile) error {
	info, err := os.Stat(file.Path)
	if err != nil {
		return err
	}
	if info.Size() != file.Size || info.ModTime().UnixNano() != file.Modified {
		return fmt.Errorf("数据文件已变化，请重建索引：%s", file.Rel)
	}
	return nil
}

func (a *auctionIndex) ready(files []sourceFile) error {
	rows, err := a.db.Query("SELECT rel,size,modified FROM files")
	if err != nil {
		return err
	}
	saved := map[string][2]int64{}
	for rows.Next() {
		var rel string
		var size, modified int64
		if err := rows.Scan(&rel, &size, &modified); err != nil {
			rows.Close()
			return err
		}
		saved[rel] = [2]int64{size, modified}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(saved) != len(files) {
		return fmt.Errorf("竞价文件清单变化，请先 -prepare-data")
	}
	for _, file := range files {
		if saved[file.Rel] != [2]int64{file.Size, file.Modified} {
			return fmt.Errorf("竞价索引未就绪：%s", file.Rel)
		}
		if err := sameSource(file); err != nil {
			return err
		}
	}
	a.files = files
	return nil
}

func (a *auctionIndex) prepare(ctx context.Context, files []sourceFile, progress func(string)) error {
	valid := map[string]bool{}
	for i, file := range files {
		valid[file.Rel] = true
		var size, modified int64
		err := a.db.QueryRowContext(ctx, "SELECT size,modified FROM files WHERE rel=?", file.Rel).Scan(&size, &modified)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil && size == file.Size && modified == file.Modified {
			if err := sameSource(file); err != nil {
				return err
			}
			progress(fmt.Sprintf("[%d/%d] cached %s", i+1, len(files), file.Rel))
			continue
		}
		progress(fmt.Sprintf("[%d/%d] indexing %s (%.1f MB)", i+1, len(files), file.Rel, float64(file.Size)/1e6))
		if err := a.indexFile(ctx, file); err != nil {
			return fmt.Errorf("%s: %w", file.Rel, err)
		}
	}
	rows, err := a.db.QueryContext(ctx, "SELECT rel FROM files")
	if err != nil {
		return err
	}
	var removed []string
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			rows.Close()
			return err
		}
		if !valid[rel] {
			removed = append(removed, rel)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, rel := range removed {
		if _, err := a.db.ExecContext(ctx, "DELETE FROM files WHERE rel=?", rel); err != nil {
			return err
		}
	}
	if err := a.ready(files); err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

func validateAuction(record []string) (string, time.Time, error) {
	if len(record) != len(auctionHeaders) {
		return "", time.Time{}, fmt.Errorf("竞价字段数量无效")
	}
	parts := strings.Split(record[0], ".")
	if len(parts) != 2 {
		return "", time.Time{}, fmt.Errorf("竞价股票代码无效")
	}
	symbol := strings.ToLower(parts[1]) + parts[0]
	if !symbolPattern.MatchString(symbol) {
		return "", time.Time{}, fmt.Errorf("竞价股票代码无效")
	}
	stamp, err := time.ParseInLocation("2006-01-02 15:04:05", record[1], beijing)
	if err != nil {
		return "", time.Time{}, err
	}
	for i := 2; i < len(record); i++ {
		if _, err := numeric(record[i], true); err != nil {
			return "", time.Time{}, fmt.Errorf("%s %s 字段 %s 无效", record[0], record[1], auctionHeaders[i])
		}
	}
	return symbol, stamp, nil
}

func (a *auctionIndex) indexFile(ctx context.Context, file sourceFile) error {
	if err := sameSource(file); err != nil {
		return err
	}
	f, err := os.Open(file.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.ReuseRecord = true
	h, err := r.Read()
	if err != nil {
		return err
	}
	if strings.TrimPrefix(strings.Join(h, ","), "\ufeff") != strings.Join(auctionHeaders, ",") {
		return fmt.Errorf("竞价CSV表头无效")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM files WHERE rel=?", file.Rel); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO files(rel,size,modified,records,first_time,last_time) VALUES(?,?,?,0,'','')", file.Rel, file.Size, file.Modified)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, "INSERT INTO blocks(file_id,symbol,day,start,end) VALUES(?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer insert.Close()
	var symbol, day, first, last string
	var blockStart, blockEnd, count int64
	flush := func() error {
		if symbol == "" {
			return nil
		}
		_, err := insert.ExecContext(ctx, id, symbol, day, blockStart, blockEnd)
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := r.InputOffset()
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("byte %d: %w", start, err)
		}
		code, _, err := validateAuction(record)
		if err != nil {
			return fmt.Errorf("byte %d: %w", start, err)
		}
		date := record[1][:10]
		if code != symbol || date != day {
			if err := flush(); err != nil {
				return err
			}
			symbol, day, blockStart = code, date, start
		}
		blockEnd = r.InputOffset()
		count++
		if first == "" || record[1] < first {
			first = record[1]
		}
		if record[1] > last {
			last = record[1]
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := sameSource(file); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE files SET records=?,first_time=?,last_time=? WHERE id=?", count, first, last, id); err != nil {
		return err
	}
	return tx.Commit()
}

type auctionResult struct {
	Symbol   string           `json:"symbol"`
	Source   string           `json:"source"`
	Timezone string           `json:"timezone"`
	Units    string           `json:"units"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Total    int              `json:"total"`
	NextPage *int             `json:"next_page"`
	Data     []map[string]any `json:"data"`
}

func (a *auctionIndex) query(ctx context.Context, symbol string, start, end time.Time, page, pageSize int) (auctionResult, error) {
	if a == nil {
		return auctionResult{}, fmt.Errorf("竞价索引未就绪")
	}
	if !symbolPattern.MatchString(symbol) || start.After(end) || page < 0 || pageSize < 1 || pageSize > 5000 {
		return auctionResult{}, fmt.Errorf("代码、时间范围或分页参数无效")
	}
	for _, file := range a.files {
		if err := sameSource(file); err != nil {
			return auctionResult{}, err
		}
	}
	rows, err := a.db.QueryContext(ctx, `SELECT f.rel,b.start,b.end FROM blocks b JOIN files f ON f.id=b.file_id WHERE b.symbol=? AND b.day>=? AND b.day<=? ORDER BY b.day,f.rel,b.start`, symbol, start.In(beijing).Format("2006-01-02"), end.In(beijing).Format("2006-01-02"))
	if err != nil {
		return auctionResult{}, err
	}
	type block struct {
		rel        string
		start, end int64
	}
	var blocks []block
	for rows.Next() {
		var b block
		if err := rows.Scan(&b.rel, &b.start, &b.end); err != nil {
			rows.Close()
			return auctionResult{}, err
		}
		blocks = append(blocks, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return auctionResult{}, err
	}
	values := map[string]map[string]any{}
	origins := map[string]string{}
	for _, b := range blocks {
		if err := ctx.Err(); err != nil {
			return auctionResult{}, err
		}
		f, err := os.Open(filepath.Join(a.root, filepath.FromSlash(b.rel)))
		if err != nil {
			return auctionResult{}, err
		}
		r := csv.NewReader(io.NewSectionReader(f, b.start, b.end-b.start))
		r.ReuseRecord = true
		r.FieldsPerRecord = len(auctionHeaders)
		for {
			record, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return auctionResult{}, err
			}
			code, stamp, err := validateAuction(record)
			if err != nil {
				f.Close()
				return auctionResult{}, err
			}
			if code != symbol {
				f.Close()
				return auctionResult{}, fmt.Errorf("竞价索引与文件不匹配，请重建")
			}
			if stamp.Before(start) || stamp.After(end) {
				continue
			}
			row := map[string]any{"time": stamp.Format(time.RFC3339), "code": record[0], "trade_date": record[1]}
			for i := 2; i < len(record); i++ {
				row[auctionHeaders[i]], _ = numeric(record[i], true)
			}
			if err := mergeRow(values, origins, row, b.rel); err != nil {
				f.Close()
				return auctionResult{}, err
			}
		}
		f.Close()
	}
	for _, file := range a.files {
		if err := sameSource(file); err != nil {
			return auctionResult{}, err
		}
	}
	all := sortedRows(values)
	total := len(all)
	begin := total
	if page <= total/pageSize {
		begin = page * pageSize
	}
	if begin > total {
		begin = total
	}
	finish := begin + pageSize
	if finish > total {
		finish = total
	}
	var next *int
	if finish < total {
		value := page + 1
		next = &value
	}
	return auctionResult{Symbol: symbol, Source: "csv_auction", Timezone: "Asia/Shanghai", Units: "unknown: original provider values", Page: page, PageSize: pageSize, Total: total, NextPage: next, Data: all[begin:finish]}, nil
}
