package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sourceFile struct {
	Path     string
	Rel      string
	Size     int64
	Modified int64
}

type localStore struct {
	root         string
	stocks       map[string][]sourceFile
	indices      map[string][]sourceFile
	names        map[string]string
	auctionFiles []sourceFile
	auction      *auctionIndex
}

func dataKey(symbol, period string) string { return symbol + "/" + period }

func discoverData(root string) (localStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return localStore{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return localStore{}, fmt.Errorf("data-root 必须是存在的目录")
	}
	s := localStore{root: abs, stocks: map[string][]sourceFile{}, indices: map[string][]sourceFile{}, names: map[string]string{}}
	byFolder := map[string]string{}
	for p, folder := range periods {
		byFolder[folder] = p
	}
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".csv" {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		if len(parts) < 2 {
			return fmt.Errorf("未识别CSV位置：%s", rel)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("不读取符号链接CSV：%s", rel)
		}
		if parts[0] == "分钟K线-指数" && d.Name() == "对应名称.csv" {
			return s.readIndexNames(path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		file := sourceFile{Path: path, Rel: rel, Size: info.Size(), Modified: info.ModTime().UnixNano()}
		if parts[0] == "集合竞价" {
			s.auctionFiles = append(s.auctionFiles, file)
			return nil
		}
		period := byFolder[filepath.Base(filepath.Dir(path))]
		symbol := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		if period == "" || !symbolPattern.MatchString(symbol) {
			return fmt.Errorf("未识别行情CSV：%s", rel)
		}
		switch parts[0] {
		case "A股个股":
			s.stocks[dataKey(symbol, period)] = append(s.stocks[dataKey(symbol, period)], file)
		case "分钟K线-指数":
			s.indices[dataKey(symbol, period)] = append(s.indices[dataKey(symbol, period)], file)
		default:
			return fmt.Errorf("未识别数据类别：%s", rel)
		}
		return nil
	})
	if err != nil {
		return localStore{}, err
	}
	// WalkDir's lexical order places historical stock packages before current year packages.
	return s, nil
}

func (s *localStore) readIndexNames(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	h, err := r.Read()
	if err != nil {
		return err
	}
	if len(h) != 3 || strings.TrimPrefix(h[0], "\ufeff") != "index" || h[1] != "code" || h[2] != "name" {
		return fmt.Errorf("指数名称表头无效")
	}
	for {
		v, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		s.names[v[1]] = v[2]
	}
}

func (s localStore) read(symbol, period string, start, end time.Time) ([]map[string]any, error) {
	period, err := normalizePeriod(symbol, period)
	if err != nil {
		return nil, err
	}
	rows := map[string]map[string]any{}
	found := false
	for _, file := range s.stocks[dataKey(symbol, period)] {
		values, err := readBars(file.Path, start, end)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.Rel, err)
		}
		found = true
		for _, row := range values {
			rows[row["time"].(string)] = row
		}
	}
	if !found {
		return nil, os.ErrNotExist
	}
	return sortedRows(rows), nil
}

func sortedRows(rows map[string]map[string]any) []map[string]any {
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i]["time"].(string) < result[j]["time"].(string) })
	return result
}

func equivalentRows(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		other, ok := b[k]
		if !ok {
			return false
		}
		if number, ok := v.(json.Number); ok {
			n, ok := other.(json.Number)
			if !ok {
				return false
			}
			x, ok1 := new(big.Rat).SetString(string(number))
			y, ok2 := new(big.Rat).SetString(string(n))
			if !ok1 || !ok2 || x.Cmp(y) != 0 {
				return false
			}
		} else if v != other {
			return false
		}
	}
	return true
}

func mergeRow(rows map[string]map[string]any, origins map[string]string, row map[string]any, rel string) error {
	key := row["time"].(string)
	if previous, ok := rows[key]; ok && !equivalentRows(previous, row) {
		return fmt.Errorf("数据冲突 %s：%s / %s", key, origins[key], rel)
	}
	rows[key] = row
	origins[key] = rel
	return nil
}

func numeric(value string, nullable bool) (any, error) {
	if value == "" && nullable {
		return nil, nil
	}
	if len(value) == 0 || strings.TrimSpace(value) != value || (value[0] != '-' && (value[0] < '0' || value[0] > '9')) || !json.Valid([]byte(value)) {
		return nil, fmt.Errorf("无效数值")
	}
	return json.Number(value), nil
}

type indexInfo struct {
	Symbol  string   `json:"symbol"`
	Name    string   `json:"name"`
	Periods []string `json:"periods"`
}

func (s localStore) searchIndices(query string) []indexInfo {
	available := map[string]map[string]bool{}
	for key := range s.indices {
		parts := strings.Split(key, "/")
		if available[parts[0]] == nil {
			available[parts[0]] = map[string]bool{}
		}
		available[parts[0]][parts[1]] = true
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := []indexInfo{}
	for symbol, ps := range available {
		name := s.names[symbol]
		if !strings.Contains(strings.ToLower(symbol+" "+name), query) {
			continue
		}
		item := indexInfo{Symbol: symbol, Name: name, Periods: []string{}}
		for _, p := range []string{"1m", "5m", "15m", "30m", "60m"} {
			if ps[p] {
				item.Periods = append(item.Periods, p)
			}
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out
}

func (s localStore) indexBars(symbol, period string, start, end time.Time) ([]map[string]any, error) {
	period, err := normalizePeriod(symbol, period)
	if err != nil {
		return nil, err
	}
	if start.After(end) {
		return nil, fmt.Errorf("开始时间不能晚于结束时间")
	}
	files := s.indices[dataKey(symbol, period)]
	if len(files) == 0 {
		return []map[string]any{}, nil
	}
	rows := map[string]map[string]any{}
	origins := map[string]string{}
	for _, file := range files {
		if err := readIndexFile(file, start, end, func(row map[string]any) error { return mergeRow(rows, origins, row, file.Rel) }); err != nil {
			return nil, fmt.Errorf("%s: %w", file.Rel, err)
		}
	}
	return sortedRows(rows), nil
}

func readIndexFile(file sourceFile, start, end time.Time, accept func(map[string]any) error) error {
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
	if strings.TrimPrefix(strings.Join(h, ","), "\ufeff") != "日期,时间,开盘,最高,最低,收盘,成交量,成交额" {
		return fmt.Errorf("指数CSV表头无效")
	}
	for {
		v, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		stamp, err := time.ParseInLocation("2006-01-02 15:04", v[0]+" "+v[1], beijing)
		if err != nil {
			return err
		}
		var numbers [6]any
		for i := range numbers {
			numbers[i], err = numeric(v[i+2], false)
			if err != nil {
				return fmt.Errorf("%s %s: %w", v[0], v[1], err)
			}
		}
		if stamp.Before(start) || stamp.After(end) {
			continue
		}
		row := map[string]any{"time": stamp.Format(time.RFC3339)}
		for i, key := range []string{"open", "high", "low", "close", "volume", "amount"} {
			row[key] = numbers[i]
		}
		if err := accept(row); err != nil {
			return err
		}
	}
}
