package cli

import (
	"bytes"
	"testing"

	"go-stock/backend/models"
)

type stockQueryFixture struct {
	quoteCodes  []string
	searchWords string
	fingerprint string
	pageSize    int
}

func (f *stockQueryFixture) GetStockCodeRealTimeData(codes ...string) (*[]models.StockInfo, error) {
	f.quoteCodes = append([]string(nil), codes...)
	items := []models.StockInfo{{Code: codes[0], Name: "测试股票", Price: "10.00"}}
	return &items, nil
}

func (f *stockQueryFixture) SearchStockWithFingerprint(words, fingerprint string, pageSize int) map[string]any {
	f.searchWords, f.fingerprint, f.pageSize = words, fingerprint, pageSize
	return map[string]any{"code": 0, "data": []any{}}
}

func TestQueryCommandsUseInjectedNarrowServices(t *testing.T) {
	stocks := &stockQueryFixture{}
	var output bytes.Buffer
	if err := runQuote([]string{"--code", "sh600000", "--json"}, GlobalOptions{}, &output, &bytes.Buffer{}, stocks); err != nil {
		t.Fatal(err)
	}
	if len(stocks.quoteCodes) != 1 || stocks.quoteCodes[0] != "sh600000" || output.Len() == 0 {
		t.Fatalf("quote codes=%v output=%q", stocks.quoteCodes, output.String())
	}

	output.Reset()
	fingerprints := &fingerprintResolverFixture{value: "saved-fingerprint"}
	if err := runSearch([]string{"--words", "银行", "--page-size", "20", "--json"}, GlobalOptions{}, &output, &bytes.Buffer{}, stocks, fingerprints); err != nil {
		t.Fatal(err)
	}
	if stocks.searchWords != "银行" || stocks.fingerprint != "saved-fingerprint" || stocks.pageSize != 20 || output.Len() == 0 {
		t.Fatalf("search=%q fingerprint=%q pageSize=%d output=%q", stocks.searchWords, stocks.fingerprint, stocks.pageSize, output.String())
	}
}
