package data

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/logger"
	appconfig "go-stock/internal/config"
)

type diemengDailyDumpReq struct {
	Date  string `json:"date"`
	Level string `json:"level,omitempty"`
}

type diemengDailyDumpEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

var (
	diemengDumpWarmMu   sync.Mutex
	diemengDumpWarmSeen = map[string]time.Time{} // YYYY-MM-DD -> last attempt
)

func warmupDiemengMinuteBarsByDailyDump(tradeDate time.Time, codes []string) error {
	// Only warm up when minute provider is (primary) diemeng (or auto, where
	// Diemeng is still the primary provider).
	provider := appconfig.Load().Minute.Provider
	if provider != "diemeng" && provider != "auto" {
		return nil
	}

	if len(codes) == 0 {
		return nil
	}

	loc := cnLocation()
	day := time.Date(tradeDate.In(loc).Year(), tradeDate.In(loc).Month(), tradeDate.In(loc).Day(), 0, 0, 0, 0, loc)
	dayKey := day.Format("2006-01-02")

	// Avoid repeated heavy downloads when the user clicks manual download multiple times.
	diemengDumpWarmMu.Lock()
	if last, ok := diemengDumpWarmSeen[dayKey]; ok && time.Since(last) < 10*time.Minute {
		diemengDumpWarmMu.Unlock()
		return nil
	}
	diemengDumpWarmSeen[dayKey] = time.Now()
	diemengDumpWarmMu.Unlock()

	want := make(map[string]struct{}, len(codes))
	for _, raw := range codes {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		want[code] = struct{}{}
	}
	if len(want) == 0 {
		return nil
	}

	client := newDiemengClient()
	apiKey := strings.TrimSpace(diemengAPIKey())
	if apiKey == "" {
		return fmt.Errorf("missing GO_STOCK_DIEMENG_API_KEY")
	}

	// daily_dump only supports recent data (documented as "last 90 days").
	// Keep the request simple: always use 1min for yield trigger accuracy.
	reqBody := diemengDailyDumpReq{Date: dayKey, Level: "1min"}

	logger.SugaredLogger.Infof("diemeng daily_dump warmup date=%s codes=%d", dayKey, len(want))

	waitForDiemengFetchWindow()
	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetHeader("apiKey", apiKey).
		SetBody(reqBody).
		Post("/stock/daily_dump")
	if err != nil {
		diemengCircuitRecordFailure(err)
		return fmt.Errorf("diemeng daily_dump request failed: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("diemeng daily_dump empty response")
	}
	if resp.RawResponse == nil {
		return fmt.Errorf("diemeng daily_dump empty raw response")
	}

	body := resp.RawBody()
	if body == nil {
		return fmt.Errorf("diemeng daily_dump empty body")
	}
	defer func() { _ = body.Close() }()

	// daily_dump returns "Content-Encoding: gzip" and the body is gzip bytes.
	// However, depending on transport behavior it may already be decompressed.
	br := bufio.NewReader(body)
	peek, _ := br.Peek(2)
	reader := io.Reader(br)
	var gz *gzip.Reader
	if len(peek) == 2 && peek[0] == 0x1f && peek[1] == 0x8b {
		gz, err = gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("diemeng daily_dump gzip reader failed: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}

	dec := json.NewDecoder(reader)
	inserted, parseErr := decodeDiemengDailyDumpToMinuteCache(dec, day, want)
	if parseErr != nil {
		diemengCircuitRecordFailure(parseErr)
		return parseErr
	}
	if inserted > 0 {
		diemengCircuitRecordSuccess()
	}
	return nil
}

func decodeDiemengDailyDumpToMinuteCache(dec *json.Decoder, day time.Time, want map[string]struct{}) (int, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, fmt.Errorf("decode diemeng daily_dump header failed: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, fmt.Errorf("invalid diemeng daily_dump json: expected object")
	}

	code := 0
	msg := ""
	inserted := 0

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return inserted, fmt.Errorf("decode diemeng daily_dump key failed: %w", err)
		}
		key, _ := keyTok.(string)
		switch key {
		case "code":
			if err := dec.Decode(&code); err != nil {
				return inserted, fmt.Errorf("decode diemeng daily_dump code failed: %w", err)
			}
		case "msg":
			if err := dec.Decode(&msg); err != nil {
				return inserted, fmt.Errorf("decode diemeng daily_dump msg failed: %w", err)
			}
		case "data":
			// data is a big object: Map<StockCode, List<[HH:MM, O,H,L,C,Vol,Amount]>>
			ins, err := decodeDiemengDailyDumpDataObject(dec, day, want)
			inserted += ins
			if err != nil {
				return inserted, err
			}
		default:
			if err := skipJSONValue(dec); err != nil {
				return inserted, err
			}
		}
	}
	// consume closing "}"
	_, _ = dec.Token()

	if code != 200 {
		text := strings.TrimSpace(msg)
		if text == "" {
			text = "unknown error"
		}
		return inserted, fmt.Errorf("diemeng daily_dump api error (code=%d): %s", code, text)
	}
	return inserted, nil
}

func decodeDiemengDailyDumpDataObject(dec *json.Decoder, day time.Time, want map[string]struct{}) (int, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, fmt.Errorf("decode diemeng daily_dump data failed: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, fmt.Errorf("invalid diemeng daily_dump data: expected object")
	}

	loc := cnLocation()
	day = time.Date(day.In(loc).Year(), day.In(loc).Month(), day.In(loc).Day(), 0, 0, 0, 0, loc)

	inserted := 0
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return inserted, fmt.Errorf("decode diemeng daily_dump stock key failed: %w", err)
		}
		stockCode, _ := keyTok.(string)
		stockCode = strings.ToUpper(strings.TrimSpace(stockCode))
		if stockCode == "" {
			if err := skipJSONValue(dec); err != nil {
				return inserted, err
			}
			continue
		}
		if _, ok := want[stockCode]; !ok {
			if err := skipJSONValue(dec); err != nil {
				return inserted, err
			}
			continue
		}

		// Decode only requested codes to avoid huge memory usage.
		var rows [][]any
		if err := dec.Decode(&rows); err != nil {
			return inserted, fmt.Errorf("decode diemeng daily_dump rows failed (code=%s): %w", stockCode, err)
		}
		bars := make([]minuteBar, 0, len(rows))
		for _, row := range rows {
			if len(row) < 6 {
				continue
			}
			hhmm, _ := row[0].(string)
			hour, minute, ok := parseHHMM(hhmm)
			if !ok {
				continue
			}
			tradeTime := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
			open := toFloatAny(row, 1)
			high := toFloatAny(row, 2)
			low := toFloatAny(row, 3)
			closeP := toFloatAny(row, 4)
			vol := toFloatAny(row, 5)
			amount := toFloatAny(row, 6)
			bars = append(bars, minuteBar{
				TradeTime: tradeTime,
				Open:      open,
				High:      high,
				Low:       low,
				Close:     closeP,
				Volume:    vol,
				Amount:    amount,
			})
		}
		if len(bars) > 0 {
			n, upsertErr := upsertMinuteBarsToCache(stockCode, bars, "diemeng_dump")
			if upsertErr != nil {
				return inserted, fmt.Errorf("upsert minute bars from daily_dump failed (code=%s): %w", stockCode, upsertErr)
			}
			inserted += n
		}
		delete(want, stockCode)
		// If we already satisfied all requested codes, we can skip the rest quickly.
		if len(want) == 0 {
			// Drain remaining keys/values in this object without decoding.
			for dec.More() {
				_, err = dec.Token()
				if err != nil {
					return inserted, err
				}
				if err := skipJSONValue(dec); err != nil {
					return inserted, err
				}
			}
			break
		}
	}

	// consume closing "}"
	_, _ = dec.Token()
	return inserted, nil
}

func parseHHMM(raw string) (int, int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, false
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func toFloatAny(row []any, idx int) float64 {
	if idx < 0 || idx >= len(row) {
		return 0
	}
	switch v := row[idx].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	default:
		return 0
	}
}

func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		// scalar value already consumed
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			// key
			if _, err := dec.Token(); err != nil {
				return err
			}
			// value
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token() // consume '}'
		return err
	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token() // consume ']'
		return err
	default:
		// should not happen
		return nil
	}
}
