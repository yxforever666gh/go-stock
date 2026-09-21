package main

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func historyFixture(t *testing.T, root, period, symbol string, records ...string) string {
	t.Helper()
	path := filepath.Join(root, periods[period], symbol+".csv")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(headers, ",")+"\n"+strings.Join(records, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func historyRecord(stamp, close string) string {
	return stamp + ",10,11,9," + close + ",22500,2169410,-0.02,-0.02074043347505963,0.002492334989894246,902767890,21687215000"
}

func fixtureStore(t *testing.T, roots ...string) localStore {
	t.Helper()
	s := localStore{stocks: map[string][]sourceFile{}, indices: map[string][]sourceFile{}, names: map[string]string{}}
	for _, root := range roots {
		for p, folder := range periods {
			paths, err := filepath.Glob(filepath.Join(root, folder, "*.csv"))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				symbol := strings.TrimSuffix(filepath.Base(path), ".csv")
				s.stocks[dataKey(symbol, p)] = append(s.stocks[dataKey(symbol, p)], sourceFile{Path: path, Rel: filepath.Base(root) + "/" + folder + "/" + filepath.Base(path), Size: info.Size(), Modified: info.ModTime().UnixNano()})
			}
		}
	}
	return s
}

func TestHistoryQueries(t *testing.T) {
	root := t.TempDir()
	historical := filepath.Join(root, "A股个股", "1分钟(2000-2025)")
	current := filepath.Join(root, "A股个股", "2026_packet", "2026")
	historyFixture(t, historical, "1m", "sh600941", historyRecord("2025-12-31 15:00:00", "10"), historyRecord("2025-08-12 13:45:00", "9"))
	historyFixture(t, current, "1m", "sh600941", historyRecord("2026-01-05 09:30:00", "12"), historyRecord("2025-12-31 15:00:00", "11"))
	s, err := discoverData(root)
	if err != nil {
		t.Fatal(err)
	}
	h := handler(s, nil, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/bars?symbol=sh600941&period=1m&start=2025-12-31&end=2026-01-05", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result response
	d := json.NewDecoder(w.Body)
	d.UseNumber()
	if err := d.Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 2 || result.Data[0]["close"] != json.Number("11") || result.Data[1]["time"] != "2026-01-05T09:30:00+08:00" {
		t.Fatal(result)
	}
	for _, source := range []string{"local", "auto"} {
		single := mcpCall(t, h, "get_bar", map[string]any{"symbol": "sh600941", "time": "2025-08-12 13:45", "source": source}, false)
		if single["source"] != "csv" || single["bar"].(map[string]any)["close"] != json.Number("9") {
			t.Fatal(single)
		}
		rangeResult := mcpCall(t, h, "get_bars", map[string]any{"symbol": "sh600941", "start_time": "2025-12-31 15:00", "end_time": "2026-01-05 09:30", "source": source}, false)
		if len(rangeResult["data"].([]any)) != 2 {
			t.Fatal(rangeResult)
		}
	}
}

func TestHistoryMissingFilesAndPeriods(t *testing.T) {
	historical, current := t.TempDir(), t.TempDir()
	historyFixture(t, historical, "1m", "sh600941", historyRecord("2025-08-12 13:45:00", "9"))
	historyFixture(t, current, "1m", "sh600000", historyRecord("2026-01-05 09:30:00", "12"))
	s := fixtureStore(t, historical, current)
	for _, symbol := range []string{"sh600941", "sh600000"} {
		if rows, err := s.read(symbol, "1m", time.Time{}, time.Time{}); err != nil || len(rows) != 1 {
			t.Fatal(symbol, rows, err)
		}
	}
	if _, err := s.read("sh600001", "1m", time.Time{}, time.Time{}); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	start, _ := date("2026-01-01")
	end, _ := date("2026-01-02")
	if rows, err := s.read("sh600941", "1m", start, end); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	for _, p := range []string{"5m", "15m", "30m", "60m"} {
		historyFixture(t, current, p, "sh600941", historyRecord("2026-01-05 09:30:00", "12"))
	}
	s = fixtureStore(t, historical, current)
	for _, p := range []string{"5m", "15m", "30m", "60m"} {
		if rows, err := s.read("sh600941", p, time.Time{}, time.Time{}); err != nil || len(rows) != 1 {
			t.Fatal(p, rows, err)
		}
	}
}

func TestHistoryValidationOutsideQuery(t *testing.T) {
	historical, current := t.TempDir(), t.TempDir()
	historyFixture(t, current, "1m", "sh600941", historyRecord("2026-01-05 09:30:00", "12"))
	start, _ := date("2026-01-05")
	for _, bad := range []string{"null", "true", "\"\"\"12\"\"\"", "12 ", "01", "NaN"} {
		historyFixture(t, historical, "1m", "sh600941", historyRecord("2025-08-12 13:45:00", bad))
		if _, err := fixtureStore(t, historical, current).read("sh600941", "1m", start, start); err == nil {
			t.Fatal("corrupt historical row ignored", bad)
		}
	}
	historyFixture(t, historical, "1m", "sh600941", historyRecord("2025-08-12 13:45:00", "9"))
	historyFixture(t, current, "1m", "sh600941", "short,row")
	if _, err := fixtureStore(t, historical, current).read("sh600941", "1m", start, start); err == nil {
		t.Fatal("corrupt current row ignored")
	}
}
