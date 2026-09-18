package research2

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go-stock/internal/trading"
)

// Every slot has its own durable daily sell receipt. Only lots bought before
// its scheduled boundary are eligible, including during restart recovery.
func (s *TradingService) processSlotSells(ctx context.Context, now time.Time) error {
	for _, slot := range Slots() {
		scheduled := SlotTime(now, slot)
		if now.Before(scheduled) {
			continue
		}
		repository := s.repository.WithSlot(slot)
		chain, err := repository.EnsureExecutionChain(ctx, now.Format("2006-01-02"), scheduled, now)
		if err != nil {
			return err
		}
		if chain.SellCompletedAt != nil {
			continue
		}
		var items []Recommendation
		if err = repository.accountQuery(ctx).Where("status IN ? AND buy_at < ?", []string{"active", "sell_pending"}, scheduled).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			quoteCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			snapshot, quoteErr := s.market.PriceAt(quoteCtx, item.StockCode, now, true)
			cancel()
			checked := s.now().In(shanghai())
			if !continuousAuction(checked) {
				return nil
			}
			stale := quoteErr != nil || !validPrice(snapshot.Price) || !currentBuyQuoteFresh(snapshot.At, checked)
			if stale {
				snapshot = PriceSnapshot{Price: item.CurrentPrice, Source: "stored_current_price"}
				if item.CurrentPriceAt != nil {
					snapshot.At = *item.CurrentPriceAt
				}
				if !validPrice(snapshot.Price) {
					snapshot.Price = item.BuyMarketPrice
					snapshot.Source = "original_buy_market_price"
					if item.BuyAt != nil {
						snapshot.At = *item.BuyAt
					}
				}
				if !validPrice(snapshot.Price) {
					snapshot.Price = item.BuyPrice
					snapshot.Source = "original_buy_execution_price"
				}
			}
			if !validPrice(snapshot.Price) {
				return fmt.Errorf("%s has no valid simulated sell price", item.RecommendationID)
			}
			cost := trading.CalculateSellCost(snapshot.Price, item.Quantity)
			mode := "scheduled_slot_sell"
			if checked.Sub(scheduled) >= time.Minute {
				mode = "recovered_slot_sell"
			}
			trade := Trade{TradeID: uuid.NewString(), Slot: slot, RecommendationID: item.RecommendationID, Side: "sell", TradedAt: checked, MarketPrice: snapshot.Price, ExecutionPrice: cost.ExecutionPrice, Quantity: item.Quantity, Commission: cost.Commission, StampDuty: cost.StampDuty, TransferFee: cost.TransferFee, SlippageAmount: cost.SlippageAmount, NetCashFlow: cost.NetCashFlow, PriceSource: snapshot.Source, ExecutionMode: mode, QuoteAt: &snapshot.At, PriceStale: stale}
			if err = repository.RecordSell(ctx, item.RecommendationID, trade); err != nil {
				return err
			}
		}
		completed := s.now().In(shanghai())
		if err = repository.db.WithContext(ctx).Model(&ExecutionChain{}).Where("chain_id = ? AND sell_completed_at IS NULL", chain.ChainID).Update("sell_completed_at", completed).Error; err != nil {
			return err
		}
		if _, err = repository.SaveSnapshot(ctx, "scheduled_sell", completed); err != nil {
			return err
		}
	}
	return nil
}
