package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBars(t *testing.T) {
	root := t.TempDir()
	row := func(stamp string) string {
		return stamp + ",96.43,96.43,96.41,96.41,22500,2169410,-0.02,-0.02074043347505963,0.002492334989894246,902767890,21687215000\n"
	}
	for _, folder := range periods {
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
		content := "\ufeff" + strings.Join(headers, ",") + "\n" + row("2026-08-13 00:00:00") + row("2026-08-12 13:45:00") + row("2026-08-12 00:00:00")
		if err := os.WriteFile(filepath.Join(root, folder, "sh600941.csv"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := handler(fixtureStore(t, root), nil, nil)
	for _, period := range []string{"", "1m", "5m", "15m", "30m", "60m"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/bars?symbol=sh600941&period="+period+"&start=2026-08-12&end=2026-08-12", nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var got response
		decoder := json.NewDecoder(w.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if len(got.Data) != 2 || got.Data[0]["time"] != "2026-08-12T00:00:00+08:00" || got.Data[1]["time"] != "2026-08-12T13:45:00+08:00" {
			t.Fatalf("unexpected rows: %+v", got)
		}
		values := strings.Split(strings.TrimSpace(row("unused")), ",")[1:]
		for i, field := range fields {
			if got.Data[1][field] != json.Number(values[i]) {
				t.Fatalf("%s: %v", field, got.Data[1][field])
			}
		}
		if got.Symbol != "sh600941" || got.Timezone != "Asia/Shanghai" || (period == "" && got.Period != "1m") {
			t.Fatalf("metadata: %+v", got)
		}
	}
	for _, tc := range []struct {
		query         string
		status, count int
	}{
		{"symbol=sh600941", 200, 3},
		{"symbol=sh600941&start=2026-08-13", 200, 1},
		{"symbol=sh600941&end=2026-08-11", 200, 0},
		{"symbol=sh600000", 404, 0},
		{"symbol=../sh600941", 400, 0},
		{"", 400, 0},
		{"symbol=sh600941&period=2m", 400, 0},
		{"symbol=sh600941&start=2026-02-30", 400, 0},
		{"symbol=sh600941&start=2026-08-13&end=2026-08-12", 400, 0},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/bars?"+tc.query, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.query, w.Code)
		}
		if tc.status == 200 {
			var got response
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Data == nil || len(got.Data) != tc.count {
				t.Fatalf("%s: %+v", tc.query, got)
			}
		}
	}
	for _, content := range []string{"bad\n", strings.Join(headers, ",") + "\nshort,row\n", strings.Join(headers, ",") + "\n" + strings.ReplaceAll(row("2026-08-12 13:45:00"), "96.43", "NaN")} {
		if err := os.WriteFile(filepath.Join(root, "1分钟", "sh600941.csv"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/bars?symbol=sh600941", nil))
		if w.Code != 500 {
			t.Fatalf("bad CSV: %d", w.Code)
		}
	}
}
