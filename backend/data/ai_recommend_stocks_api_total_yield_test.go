package data

import (
	"math"
	"testing"

	"go-stock/backend/models"
)

func TestCalculateYieldTotalByItems_EqualPositionCapital(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{
		{StockCode: "000001.SZ", ActivationStatus: "activated", BuyAmount: 10, CurrentPrice: 11},
		{StockCode: "000002.SZ", ActivationStatus: "activated", BuyAmount: 20, CurrentPrice: 18},
	}

	total, totalText := calculateYieldTotalByItems(items)
	if math.Abs(total-(-0.48)) > 0.001 {
		t.Fatalf("expected total yield -0.48, got %.4f", total)
	}
	if totalText != "-0.48%" {
		t.Fatalf("expected total text -0.48%%, got %s", totalText)
	}
}

func TestCalculateYieldTotalByItems_SoldAndHold(t *testing.T) {
	sell := 12.0
	items := []models.AiRecommendStocksYieldItem{
		{StockCode: "000001.SZ", ActivationStatus: "activated", BuyAmount: 10, SellAmount: &sell},
		{StockCode: "000002.SZ", ActivationStatus: "activated", BuyAmount: 20, CurrentPrice: 19},
	}

	total, totalText := calculateYieldTotalByItems(items)
	if math.Abs(total-6.99) > 0.001 {
		t.Fatalf("expected total yield 6.99, got %.4f", total)
	}
	if totalText != "+6.99%" {
		t.Fatalf("expected total text +6.99%%, got %s", totalText)
	}
}

func TestCalculateYieldTotalByItems_IgnoresPendingItems(t *testing.T) {
	sell := 12.0
	items := []models.AiRecommendStocksYieldItem{
		{StockCode: "000001.SZ", ActivationStatus: "pending", BuyAmount: 10, SellAmount: &sell},
		{StockCode: "000002.SZ", ActivationStatus: "pending", BuyAmount: 20, CurrentPrice: 19},
	}

	total, totalText := calculateYieldTotalByItems(items)
	if math.Abs(total) > 0.001 {
		t.Fatalf("expected total yield 0, got %.4f", total)
	}
	if totalText != "--" {
		t.Fatalf("expected total text --, got %s", totalText)
	}
}

func TestCalculateYieldTotalByItems_IgnoresIneligibleItems(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{
		{
			StockCode:           "000001.SZ",
			BacktestEligibility: recommendBacktestIneligible,
			ActivationStatus:    "activated",
			BuyAmount:           10,
			CurrentPrice:        11,
		},
	}

	total, totalText := calculateYieldTotalByItems(items)
	if math.Abs(total) > 0.001 {
		t.Fatalf("expected total yield 0, got %.4f", total)
	}
	if totalText != "--" {
		t.Fatalf("expected total text --, got %s", totalText)
	}
}

func TestCalculateYieldTotalByItemsV150UsesReusablePortfolioCapital(t *testing.T) {
	items := []models.AiRecommendStocksYieldItem{
		{SummaryVersion: "1.5.0", ActivationStatus: "activated", BacktestEligibility: recommendBacktestEligible,
			V150LedgerAccountingReady: true, V150LedgerEntryCash: 9000, V150LedgerNetPnL: 100},
		{SummaryVersion: "1.5.0", ActivationStatus: "activated", BacktestEligibility: recommendBacktestEligible,
			V150LedgerAccountingReady: true, V150LedgerEntryCash: 9900, V150LedgerNetPnL: 200},
	}
	total, text := calculateYieldTotalByItems(items)
	if total != .3 || text != "+0.30%" {
		t.Fatalf("sequential V1.5 turnover inflated denominator: total=%.4f text=%q", total, text)
	}
}

func TestCalculateYieldTotalByItemsV150RejectsPartialOrMixedLedger(t *testing.T) {
	ready := models.AiRecommendStocksYieldItem{
		SummaryVersion: "1.5.0", ActivationStatus: "activated", BacktestEligibility: recommendBacktestEligible,
		V150LedgerAccountingReady: true, V150LedgerNetPnL: 100,
	}
	missing := ready
	missing.V150LedgerAccountingReady = false
	if _, text := calculateYieldTotalByItems([]models.AiRecommendStocksYieldItem{ready, missing}); text != "--" {
		t.Fatalf("partial ledger produced a portfolio result: %q", text)
	}
	legacy := ready
	legacy.SummaryVersion = "1.4.2"
	if _, text := calculateYieldTotalByItems([]models.AiRecommendStocksYieldItem{ready, legacy}); text != "--" {
		t.Fatalf("mixed cohort silently dropped one side: %q", text)
	}
}
