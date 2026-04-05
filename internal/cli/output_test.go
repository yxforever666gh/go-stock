package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"go-stock/backend/data"
)

func TestFormatQuoteText(t *testing.T) {
	item := &data.StockInfo{
		Name:          "测试股份",
		Code:          "sh600000",
		Price:         "10.23",
		ChangePercent: 1.23,
		ChangePrice:   0.12,
		Open:          "10.00",
		High:          "10.40",
		Low:           "9.90",
		Volume:        "100000",
		Amount:        "123456789",
		Date:          "2026-02-18",
		Time:          "10:30:00",
	}
	out := formatQuoteText(item)
	mustContain := []string{
		"测试股份",
		"sh600000",
		"10.23",
		"1.23%",
		"2026-02-18",
		"10:30:00",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got: %s", want, out)
		}
	}
}

func TestFormatSearchText(t *testing.T) {
	res := map[string]any{
		"code":    0,
		"message": "ok",
		"data": map[string]any{
			"list": []any{
				map[string]any{
					"code":          "600000",
					"name":          "浦发银行",
					"changePercent": "1.23",
					"turnover":      "123456",
				},
			},
		},
	}
	out := formatSearchText(res)
	mustContain := []string{"code: 0", "message: ok", "600000", "浦发银行", "1.23", "123456"}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got: %s", want, out)
		}
	}
}

func TestMarshalJSONLine(t *testing.T) {
	body, err := marshalJSONLine(map[string]any{
		"code":    1,
		"content": "ok",
	})
	if err != nil {
		t.Fatalf("marshalJSONLine failed: %v", err)
	}
	if strings.Contains(string(body), "\n") {
		t.Fatalf("json line should not contain newline: %s", string(body))
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("json line should be valid json: %v", err)
	}
}
