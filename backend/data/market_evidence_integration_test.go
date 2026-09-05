//go:build integration

package data

import (
	"context"
	"os"
	"testing"
	"time"

	"go-stock/backend/marketdata"
)

func TestFundFlowAndTimelineLiveContract(t *testing.T) {
	if os.Getenv("GO_STOCK_LIVE_EASTMONEY") != "1" {
		t.Skip("set GO_STOCK_LIVE_EASTMONEY=1 to probe the live Eastmoney fund-flow contract")
	}
	service := NewMarketEvidenceServiceWithMinuteDB(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	list := service.FundFlows(ctx, marketdata.ProviderRequest{Scope: "sector", Sort: "netamount", Limit: 3})
	if list.Status != marketdata.StatusOK || list.Source != "eastmoney-delay" || list.AsOf.IsZero() || len(list.Data) != 3 {
		t.Fatalf("fund-flow list=%+v", list)
	}
	row := list.Data[0]
	if _, ok := NormalizeFundFlowBoardCode(row.Code); !ok || row.ChangePct == nil || row.MainNetRatio == nil || row.SuperLargeNetAmount == nil || row.LargeNetAmount == nil || row.MediumNetAmount == nil || row.SmallNetAmount == nil {
		t.Fatalf("fund-flow row=%+v", row)
	}
	timeline := service.FundFlowTimeline(ctx, marketdata.ProviderRequest{Code: row.Code})
	if timeline.Status != marketdata.StatusOK || timeline.Source != "eastmoney-delay" || timeline.Data.Code != row.Code || timeline.Data.TradingDate == "" || len(timeline.Data.Points) < 100 || timeline.AsOf.IsZero() {
		t.Fatalf("fund-flow timeline=%+v", timeline)
	}
}
