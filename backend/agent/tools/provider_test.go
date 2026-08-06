package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go-stock/backend/models"
)

type fakeStockCodeProvider struct {
	query string
	rows  []models.StockBasic
}

func (p *fakeStockCodeProvider) SearchStocks(query string) []models.StockBasic {
	p.query = query
	return p.rows
}

type fakeBKDictProvider struct {
	rows []any
}

func (p fakeBKDictProvider) BoardDictionary() []any { return p.rows }

type fakeMarketNewsProvider struct {
	calendar []any
	news     []*models.Telegraph
	global   []models.Telegraph
}

func (p fakeMarketNewsProvider) MarketCalendar() []any { return p.calendar }
func (p fakeMarketNewsProvider) MarketNews(string, int) []*models.Telegraph {
	return p.news
}
func (p fakeMarketNewsProvider) TradingViewNews() []models.Telegraph { return p.global }
func (p fakeMarketNewsProvider) ReutersNews() *models.ReutersNews    { return nil }

func TestStockCodeToolUsesInjectedProvider(t *testing.T) {
	provider := &fakeStockCodeProvider{rows: []models.StockBasic{{Symbol: "600000", Name: "测试"}}}
	tool := GetQueryStockCodeInfoTool(provider)
	result, err := tool.InvokableRun(context.Background(), `{"searchWord":"测试"}`)
	if err != nil {
		t.Fatalf("invoke stock code tool: %v", err)
	}
	if provider.query != "测试" || !strings.Contains(result, "600000") {
		t.Fatalf("provider result = %q, query = %q", result, provider.query)
	}
}

func TestToolsRejectMissingProvider(t *testing.T) {
	stockResult, err := GetQueryStockCodeInfoTool(nil).InvokableRun(context.Background(), `{"searchWord":"测试"}`)
	if !errors.Is(err, ErrToolDataProviderRequired) || stockResult != "" {
		t.Fatalf("stock code nil provider result=%q err=%v", stockResult, err)
	}
	bkResult, err := GetQueryBKDictTool(nil).InvokableRun(context.Background(), `{}`)
	if !errors.Is(err, ErrToolDataProviderRequired) || bkResult != "" {
		t.Fatalf("board dictionary nil provider result=%q err=%v", bkResult, err)
	}
	newsResult, err := GetQueryMarketNewsTool(nil).InvokableRun(context.Background(), `{}`)
	if !errors.Is(err, ErrToolDataProviderRequired) || newsResult != "" {
		t.Fatalf("market news nil provider result=%q err=%v", newsResult, err)
	}
}

func TestMarketNewsToolUsesInjectedProvider(t *testing.T) {
	provider := fakeMarketNewsProvider{
		calendar: []any{map[string]any{
			"calendar_day": "2026-08-06",
			"items":        []map[string]any{{"title": "calendar-item"}},
		}},
		news:   []*models.Telegraph{{Time: "09:30", Content: "market-item"}},
		global: []models.Telegraph{{Title: "global-item"}},
	}
	result, err := GetQueryMarketNewsTool(provider).InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("invoke market news tool: %v", err)
	}
	for _, want := range []string{"market-item", "global-item", "calendar-item"} {
		if !strings.Contains(result, want) {
			t.Fatalf("market news result missing %q: %s", want, result)
		}
	}
}

func TestBoardDictionaryToolUsesInjectedProvider(t *testing.T) {
	result, err := GetQueryBKDictTool(fakeBKDictProvider{rows: []any{map[string]any{"code": "016"}}}).InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("invoke board dictionary tool: %v", err)
	}
	if !strings.Contains(result, "016") {
		t.Fatalf("board dictionary result = %s", result)
	}
}
