package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/models"
	"go-stock/backend/strategy/v150"
)

var ErrInvalidOrderEventReplay = errors.New("invalid frozen order-event replay")

// OrderEventReplayStats is a true 100,000 CNY portfolio result. NetReturnPct
// is retained as the API-compatible alias of PortfolioNetReturnPct.
type OrderEventReplayStats struct {
	PolicyValidated       bool     `json:"policyValidated"`
	ValuationMode         string   `json:"valuationMode"`
	TradeCount            int      `json:"tradeCount"`
	ClosedTradeCount      int      `json:"closedTradeCount"`
	OpenPositionCount     int      `json:"openPositionCount"`
	WinningTrades         int      `json:"winningTrades"`
	LosingTrades          int      `json:"losingTrades"`
	FlatTrades            int      `json:"flatTrades"`
	InitialCash           float64  `json:"initialCash"`
	EndingCash            float64  `json:"endingCash"`
	EndingEquity          float64  `json:"endingEquity"`
	GrossPnL              float64  `json:"grossPnl"`
	Fees                  float64  `json:"fees"`
	NetPnL                float64  `json:"netPnl"`
	EntryCash             float64  `json:"entryCash"`
	GrossProfit           float64  `json:"grossProfit"`
	GrossLoss             float64  `json:"grossLoss"`
	NetReturnPct          float64  `json:"netReturnPct"`
	PortfolioNetReturnPct float64  `json:"portfolioNetReturnPct"`
	NetMeanReturnPct      float64  `json:"netMeanReturnPct"`
	WinRatePct            float64  `json:"winRatePct"`
	ProfitFactor          *float64 `json:"profitFactor"`
	ProfitFactorText      string   `json:"profitFactorText"`
	Stress20EndingEquity  float64  `json:"stress20EndingEquity"`
	Stress20NetPnL        float64  `json:"stress20NetPnl"`
	Stress20NetReturnPct  float64  `json:"stress20NetReturnPct"`
	Stress50EndingEquity  float64  `json:"stress50EndingEquity"`
	Stress50NetPnL        float64  `json:"stress50NetPnl"`
	Stress50NetReturnPct  float64  `json:"stress50NetReturnPct"`
}

type replayPosition struct {
	entry              models.OrderEvent
	adjustedEntryPrice float64
	quantity           float64
	corporateCash      float64
	sourceEventIDs     []string
	markPrice          float64
	markAt             time.Time
}

type replayClosedPair struct {
	position replayPosition
	exit     models.OrderEvent
}

type orderEventReplayPolicy struct {
	dailyCapByRun    map[string]int
	sectorByRule     map[string]string
	metadataComplete bool
}

func buildOrderEventReplayPolicy(inputs FrozenStrategyInputs) (orderEventReplayPolicy, error) {
	policy := orderEventReplayPolicy{
		dailyCapByRun:    make(map[string]int, len(inputs.Runs)),
		sectorByRule:     make(map[string]string, len(inputs.Rules)),
		metadataComplete: true,
	}
	cfg := v150.FixedStrategyV150Config()
	for _, run := range inputs.Runs {
		var envelope struct {
			Run *struct {
				Regime struct {
					DailyCap int `json:"dailyCap"`
				} `json:"regime"`
			} `json:"run"`
		}
		if err := json.Unmarshal([]byte(run.PayloadJSON), &envelope); err != nil {
			return policy, fmt.Errorf("run %s policy payload: %w", run.RunID, err)
		}
		cap := cfg.RiskOnDailyCap
		if envelope.Run != nil {
			cap = envelope.Run.Regime.DailyCap
		} else {
			policy.metadataComplete = false
		}
		if cap < 0 || cap > cfg.RiskOnDailyCap {
			return policy, fmt.Errorf("run %s has invalid daily cap %d", run.RunID, cap)
		}
		if run.RuleCount > 0 && cap == 0 {
			return policy, fmt.Errorf("run %s issued rules with a zero daily cap", run.RunID)
		}
		policy.dailyCapByRun[run.RunID] = cap
	}

	candidateSector := make(map[string]string, len(inputs.Candidates)*2)
	for _, candidate := range inputs.Candidates {
		sector := strings.TrimSpace(candidate.Sector)
		candidateSector[candidate.CandidateID] = sector
		candidateSector[candidate.RunID+"\x00"+strings.ToUpper(strings.TrimSpace(candidate.Symbol))] = sector
	}
	for _, rule := range inputs.Rules {
		sector, ok := candidateSector[rule.CandidateID]
		if !ok {
			sector, ok = candidateSector[rule.RunID+"\x00"+strings.ToUpper(strings.TrimSpace(rule.Symbol))]
		}
		if !ok {
			return policy, fmt.Errorf("rule %s has no candidate sector metadata", rule.RuleID)
		}
		if strings.TrimSpace(sector) == "" {
			policy.metadataComplete = false
		}
		policy.sectorByRule[rule.RunID+"\x00"+rule.RuleID] = strings.TrimSpace(sector)
	}
	return policy, nil
}

// ReplayFrozenStrategyInputs revalidates the frozen run/rule/event ownership
// before applying portfolio accounting. It never reads providers or market
// data; the supplied immutable cache is the complete fact set.
func ReplayFrozenStrategyInputs(backtestID, strategyVersion string, inputs FrozenStrategyInputs, frozenAt time.Time) ([]models.Trade, OrderEventReplayStats, string, error) {
	if err := validateFrozenChildCounts(inputs); err != nil {
		return nil, OrderEventReplayStats{}, "", fmt.Errorf("%w: %v", ErrInvalidOrderEventReplay, err)
	}
	if err := validateLoadedFrozenRuns(inputs); err != nil {
		return nil, OrderEventReplayStats{}, "", fmt.Errorf("%w: %v", ErrInvalidOrderEventReplay, err)
	}
	policy, err := buildOrderEventReplayPolicy(inputs)
	if err != nil {
		return nil, OrderEventReplayStats{}, "", fmt.Errorf("%w: %v", ErrInvalidOrderEventReplay, err)
	}
	if err := validateRecommendationPublicationPolicy(inputs, policy); err != nil {
		return nil, OrderEventReplayStats{}, "", fmt.Errorf("%w: %v", ErrInvalidOrderEventReplay, err)
	}
	return replayFrozenOrderEvents(backtestID, strategyVersion, inputs.OrderEvents, frozenAt, policy)
}

type recommendationPublicationInterval struct {
	runID      string
	ruleID     string
	symbol     string
	sector     string
	issuedAt   time.Time
	terminalAt time.Time
	stopAt     time.Time
}

// validateRecommendationPublicationPolicy enforces recommendation-time
// constraints from frozen rules and their complete visible event prefixes.
// Fill-only replay is too late for daily quotas, sector concentration and a
// duplicate symbol that remains pending or held under another rule.
func validateRecommendationPublicationPolicy(inputs FrozenStrategyInputs, policy orderEventReplayPolicy) error {
	eventsByRule := make(map[string][]models.OrderEvent, len(inputs.Rules))
	for _, event := range inputs.OrderEvents {
		key := event.RunID + "\x00" + event.RuleID
		eventsByRule[key] = append(eventsByRule[key], event)
	}
	intervals := make([]recommendationPublicationInterval, 0, len(inputs.Rules))
	for _, rule := range inputs.Rules {
		key := rule.RunID + "\x00" + rule.RuleID
		interval := recommendationPublicationInterval{
			runID: rule.RunID, ruleID: rule.RuleID,
			symbol: strings.ToUpper(strings.TrimSpace(rule.Symbol)),
			sector: strings.TrimSpace(policy.sectorByRule[key]),
		}
		for _, event := range eventsByRule[key] {
			switch normalizedOrderEventType(event.EventType) {
			case "rule_issued":
				if !interval.issuedAt.IsZero() {
					return fmt.Errorf("rule %s has duplicate rule_issued facts", rule.RuleID)
				}
				interval.issuedAt = event.EventAt
			case "reject", "activation_expired", "expired":
				if interval.terminalAt.IsZero() || event.EventAt.Before(interval.terminalAt) {
					interval.terminalAt = event.EventAt
				}
			case "exit_fill":
				if interval.terminalAt.IsZero() || event.EventAt.Before(interval.terminalAt) {
					interval.terminalAt = event.EventAt
				}
				if replayIsStopReason(event.Reason) && (interval.stopAt.IsZero() || event.EventAt.Before(interval.stopAt)) {
					interval.stopAt = event.EventAt
				}
			}
		}
		if interval.symbol == "" || interval.issuedAt.IsZero() {
			return fmt.Errorf("rule %s has no symbol or rule_issued fact", rule.RuleID)
		}
		if !interval.terminalAt.IsZero() && interval.terminalAt.Before(interval.issuedAt) {
			return fmt.Errorf("rule %s terminates before publication", rule.RuleID)
		}
		intervals = append(intervals, interval)
	}

	sort.Slice(intervals, func(i, j int) bool {
		if !intervals[i].issuedAt.Equal(intervals[j].issuedAt) {
			return intervals[i].issuedAt.Before(intervals[j].issuedAt)
		}
		if intervals[i].runID != intervals[j].runID {
			return intervals[i].runID < intervals[j].runID
		}
		return intervals[i].ruleID < intervals[j].ruleID
	})
	dailyPublished := make(map[string]int)
	dailySectorPublished := make(map[string]int)
	priorBySymbol := make(map[string][]recommendationPublicationInterval)
	for _, interval := range intervals {
		cap, ok := policy.dailyCapByRun[interval.runID]
		if !ok || cap <= 0 {
			return fmt.Errorf("rule %s has no positive frozen daily cap", interval.ruleID)
		}
		day := cnDateText(interval.issuedAt)
		if dailyPublished[day] >= cap {
			return fmt.Errorf("more than %d recommendations were published on %s for run %s", cap, day, interval.runID)
		}
		sectorKey := day + "\x00" + interval.sector
		if interval.sector != "" && dailySectorPublished[sectorKey] >= v150.FixedStrategyV150Config().MaximumSectorEntriesDay {
			return fmt.Errorf("more than %d recommendations were published for sector %s on %s", v150.FixedStrategyV150Config().MaximumSectorEntriesDay, interval.sector, day)
		}
		for _, prior := range priorBySymbol[interval.symbol] {
			if prior.terminalAt.IsZero() || !interval.issuedAt.After(prior.terminalAt) {
				return fmt.Errorf("symbol %s was republished while rule %s remained pending or held", interval.symbol, prior.ruleID)
			}
			if !prior.stopAt.IsZero() {
				elapsed := replayEventTradeDayIndex(models.OrderEvent{EventAt: interval.issuedAt}) - replayEventTradeDayIndex(models.OrderEvent{EventAt: prior.stopAt})
				if elapsed < v150.FixedStrategyV150Config().StopCooldownTradeDays {
					return fmt.Errorf("symbol %s was republished before %d-trade-day stop cooldown", interval.symbol, v150.FixedStrategyV150Config().StopCooldownTradeDays)
				}
			}
		}
		dailyPublished[day]++
		if interval.sector != "" {
			dailySectorPublished[sectorKey]++
		}
		priorBySymbol[interval.symbol] = append(priorBySymbol[interval.symbol], interval)
	}
	return nil
}

// ReplayFrozenOrderEvents deterministically replays already-validated facts.
// Open fills at the cohort end are retained as open Trade rows and marked at
// the latest persisted price (or the reconstructed raw entry price).
func ReplayFrozenOrderEvents(backtestID, strategyVersion string, events []models.OrderEvent, frozenAt time.Time) ([]models.Trade, OrderEventReplayStats, string, error) {
	return replayFrozenOrderEvents(backtestID, strategyVersion, events, frozenAt, orderEventReplayPolicy{})
}

func replayFrozenOrderEvents(backtestID, strategyVersion string, events []models.OrderEvent, frozenAt time.Time, policy orderEventReplayPolicy) ([]models.Trade, OrderEventReplayStats, string, error) {
	empty := OrderEventReplayStats{}
	backtestID, strategyVersion = strings.TrimSpace(backtestID), strings.TrimSpace(strategyVersion)
	if backtestID == "" || strategyVersion == "" || frozenAt.IsZero() {
		return nil, empty, "", fmt.Errorf("%w: backtest id, strategy version and frozen time are required", ErrInvalidOrderEventReplay)
	}

	ordered := append([]models.OrderEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool {
		return orderEventFactLess(ordered[i], ordered[j])
	})
	previousSequence := map[string]int{}
	previousAt := map[string]time.Time{}
	seenEvent := map[string]struct{}{}
	open := map[string]replayPosition{}
	openSymbol := map[string]string{}
	dailyEntries := map[string]int{}
	dailySectorEntries := map[string]int{}
	lastStopTradeDayIndex := map[string]int{}
	pairs := make([]replayClosedPair, 0)
	cfg := v150.FixedStrategyV150Config()
	cash := cfg.PortfolioCash

	for i := range ordered {
		event := ordered[i]
		if err := validateReplayEvent(event, strategyVersion); err != nil {
			return nil, empty, "", err
		}
		if event.FrozenAt.After(frozenAt) {
			return nil, empty, "", fmt.Errorf("%w: replay frozen time precedes event %s freeze", ErrInvalidOrderEventReplay, event.EventID)
		}
		if _, duplicate := seenEvent[event.EventID]; duplicate {
			return nil, empty, "", fmt.Errorf("%w: duplicate event id %s", ErrInvalidOrderEventReplay, event.EventID)
		}
		seenEvent[event.EventID] = struct{}{}
		ledgerKey := event.RunID + "\x00" + event.RuleID
		if prior, ok := previousSequence[ledgerKey]; ok && event.Sequence <= prior {
			return nil, empty, "", fmt.Errorf("%w: run %s rule %q sequence %d is not increasing", ErrInvalidOrderEventReplay, event.RunID, event.RuleID, event.Sequence)
		}
		if prior := previousAt[ledgerKey]; !prior.IsZero() && event.EventAt.Before(prior) {
			return nil, empty, "", fmt.Errorf("%w: run %s rule %q event time regressed", ErrInvalidOrderEventReplay, event.RunID, event.RuleID)
		}
		previousSequence[ledgerKey], previousAt[ledgerKey] = event.Sequence, event.EventAt
		key := replayPositionKey(event)
		kind := normalizedOrderEventType(event.EventType)
		if position, exists := open[key]; exists && finitePositive(event.Price) && kind != "exit_fill" {
			position.markPrice, position.markAt = event.Price, event.EventAt
			open[key] = position
		}
		switch kind {
		case "fill":
			if !isCNTradingSession(event.EventAt) {
				return nil, empty, "", fmt.Errorf("%w: fill %s is outside A-share trading hours", ErrInvalidOrderEventReplay, event.EventID)
			}
			if err := validateFillAccounting(event, true); err != nil {
				return nil, empty, "", err
			}
			if err := validateV150FillPolicy(event, true); err != nil {
				return nil, empty, "", fmt.Errorf("%w: %v", ErrInvalidOrderEventReplay, err)
			}
			if _, exists := open[key]; exists {
				return nil, empty, "", fmt.Errorf("%w: duplicate open fill %s", ErrInvalidOrderEventReplay, event.EventID)
			}
			symbol := strings.ToUpper(strings.TrimSpace(event.Symbol))
			if owner, exists := openSymbol[symbol]; exists {
				return nil, empty, "", fmt.Errorf("%w: symbol %s already open under %s", ErrInvalidOrderEventReplay, symbol, owner)
			}
			if len(open) >= cfg.MaximumOpenPositions {
				return nil, empty, "", fmt.Errorf("%w: more than %d concurrent positions", ErrInvalidOrderEventReplay, cfg.MaximumOpenPositions)
			}
			day := cnDateText(event.EventAt)
			dailyCap := cfg.RiskOnDailyCap
			if configured, ok := policy.dailyCapByRun[event.RunID]; ok {
				dailyCap = configured
			}
			if dailyCap <= 0 || dailyEntries[day] >= dailyCap {
				return nil, empty, "", fmt.Errorf("%w: more than %d entry fills on %s for run %s", ErrInvalidOrderEventReplay, dailyCap, day, event.RunID)
			}
			sector := strings.TrimSpace(policy.sectorByRule[event.RunID+"\x00"+event.RuleID])
			sectorKey := day + "\x00" + sector
			if sector != "" && dailySectorEntries[sectorKey] >= cfg.MaximumSectorEntriesDay {
				return nil, empty, "", fmt.Errorf("%w: more than %d entry fills for sector %s on %s", ErrInvalidOrderEventReplay, cfg.MaximumSectorEntriesDay, sector, day)
			}
			tradeDayIndex := replayEventTradeDayIndex(event)
			if stoppedAt, ok := lastStopTradeDayIndex[symbol]; ok && tradeDayIndex-stoppedAt < cfg.StopCooldownTradeDays {
				return nil, empty, "", fmt.Errorf("%w: symbol %s re-entered before %d-trade-day stop cooldown", ErrInvalidOrderEventReplay, symbol, cfg.StopCooldownTradeDays)
			}
			entryCash := event.Price*event.Quantity + event.Fees
			if entryCash > cash+1e-8 {
				return nil, empty, "", fmt.Errorf("%w: fill %s exceeds available cash", ErrInvalidOrderEventReplay, event.EventID)
			}
			cash -= entryCash
			rawEntry := event.Price / (1 + cfg.BaseSlippageBPS/10_000)
			open[key] = replayPosition{
				entry: event, adjustedEntryPrice: event.Price, quantity: event.Quantity,
				sourceEventIDs: []string{event.EventID}, markPrice: rawEntry, markAt: event.EventAt,
			}
			openSymbol[symbol] = key
			dailyEntries[day]++
			if sector != "" {
				dailySectorEntries[sectorKey]++
			}
		case "corporate_action":
			position, exists := open[key]
			if !exists {
				return nil, empty, "", fmt.Errorf("%w: corporate_action %s has no matching open fill", ErrInvalidOrderEventReplay, event.EventID)
			}
			if !integerShares(event.Quantity) || event.AdjustmentFactor <= 0 || !finiteNonNegative(event.CashAmount) {
				return nil, empty, "", fmt.Errorf("%w: corporate_action %s has invalid quantity/cash/factor", ErrInvalidOrderEventReplay, event.EventID)
			}
			position.adjustedEntryPrice *= event.AdjustmentFactor
			position.quantity = event.Quantity
			position.corporateCash += event.CashAmount
			position.sourceEventIDs = append(position.sourceEventIDs, event.EventID)
			if position.markPrice > 0 {
				position.markPrice *= event.AdjustmentFactor
			}
			cash += event.CashAmount
			open[key] = position
		case "exit_fill":
			if !isCNTradingSession(event.EventAt) {
				return nil, empty, "", fmt.Errorf("%w: exit_fill %s is outside A-share trading hours", ErrInvalidOrderEventReplay, event.EventID)
			}
			if err := validateFillAccounting(event, false); err != nil {
				return nil, empty, "", err
			}
			if err := validateV150FillPolicy(event, false); err != nil {
				return nil, empty, "", fmt.Errorf("%w: %v", ErrInvalidOrderEventReplay, err)
			}
			position, exists := open[key]
			if !exists {
				return nil, empty, "", fmt.Errorf("%w: exit_fill %s has no matching open fill", ErrInvalidOrderEventReplay, event.EventID)
			}
			if !event.EventAt.After(position.entry.EventAt) || !isLaterCNDate(position.entry.EventAt, event.EventAt) {
				return nil, empty, "", fmt.Errorf("%w: exit_fill %s violates ordering or T+1", ErrInvalidOrderEventReplay, event.EventID)
			}
			if !nearlyEqual(event.Quantity, position.quantity) {
				return nil, empty, "", fmt.Errorf("%w: exit quantity differs from entry", ErrInvalidOrderEventReplay)
			}
			cash += event.Price*event.Quantity - event.Fees
			position.sourceEventIDs = append(position.sourceEventIDs, event.EventID)
			pairs = append(pairs, replayClosedPair{position: position, exit: event})
			if replayIsStopReason(event.Reason) {
				lastStopTradeDayIndex[strings.ToUpper(strings.TrimSpace(event.Symbol))] = replayEventTradeDayIndex(event)
			}
			delete(openSymbol, strings.ToUpper(strings.TrimSpace(event.Symbol)))
			delete(open, key)
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		if !pairs[i].position.entry.EventAt.Equal(pairs[j].position.entry.EventAt) {
			return pairs[i].position.entry.EventAt.Before(pairs[j].position.entry.EventAt)
		}
		return replayPositionKey(pairs[i].position.entry) < replayPositionKey(pairs[j].position.entry)
	})
	trades := make([]models.Trade, 0, len(pairs)+len(open))
	for _, pair := range pairs {
		trade, err := replayPairToTrade(backtestID, strategyVersion, len(trades)+1, pair, frozenAt.UTC())
		if err != nil {
			return nil, empty, "", err
		}
		trades = append(trades, trade)
	}
	openKeys := make([]string, 0, len(open))
	for key := range open {
		openKeys = append(openKeys, key)
	}
	sort.Strings(openKeys)
	for _, key := range openKeys {
		trade, err := replayOpenToTrade(backtestID, strategyVersion, len(trades)+1, open[key], frozenAt.UTC())
		if err != nil {
			return nil, empty, "", err
		}
		trades = append(trades, trade)
	}
	stats := calculatePortfolioReplayStats(trades, cash, ordered)
	stats.PolicyValidated = policy.metadataComplete
	stats.ValuationMode = "event_price_or_entry_cost"
	resultHash := replayResultHash(backtestID, strategyVersion, trades, stats)
	return trades, stats, resultHash, nil
}

func replayPositionKey(event models.OrderEvent) string {
	return strings.Join([]string{event.RunID, event.RuleID, strings.ToUpper(strings.TrimSpace(event.Symbol))}, "\x00")
}

func validateReplayEvent(event models.OrderEvent, strategyVersion string) error {
	if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.RunID) == "" || strings.TrimSpace(event.Symbol) == "" || strings.TrimSpace(event.EventType) == "" {
		return fmt.Errorf("%w: event identity, run, symbol and type are required", ErrInvalidOrderEventReplay)
	}
	if event.StrategyVersion != strategyVersion {
		return fmt.Errorf("%w: event %s version does not match", ErrInvalidOrderEventReplay, event.EventID)
	}
	if event.Sequence <= 0 || event.EventAt.IsZero() || event.FrozenAt == nil || event.FrozenAt.Before(event.EventAt) {
		return fmt.Errorf("%w: event %s has invalid sequence/event/frozen time", ErrInvalidOrderEventReplay, event.EventID)
	}
	if err := verifySnapshotRecord(event); err != nil {
		return fmt.Errorf("%w: event %s seal: %v", ErrInvalidOrderEventReplay, event.EventID, err)
	}
	return nil
}

func validateFillAccounting(event models.OrderEvent, requireBoardLot bool) error {
	if !finitePositive(event.Price) || !integerShares(event.Quantity) || !finiteNonNegative(event.Fees) {
		return fmt.Errorf("%w: event %s has invalid persisted price, quantity or fees", ErrInvalidOrderEventReplay, event.EventID)
	}
	if requireBoardLot && int64(event.Quantity)%100 != 0 {
		return fmt.Errorf("%w: entry fill %s is not an A-share board lot", ErrInvalidOrderEventReplay, event.EventID)
	}
	return nil
}

func replayPairToTrade(backtestID, strategyVersion string, sequence int, pair replayClosedPair, frozenAt time.Time) (models.Trade, error) {
	position, exit := pair.position, pair.exit
	entry := position.entry
	fees := entry.Fees + exit.Fees
	originalEntryNotional := entry.Price * entry.Quantity
	gross := exit.Price*position.quantity + position.corporateCash - originalEntryNotional
	net := gross - fees
	entryCash := originalEntryNotional + entry.Fees
	exitAt := exit.EventAt.UTC()
	sourceIDs, _ := json.Marshal(position.sourceEventIDs)
	payload, _ := json.Marshal(map[string]any{
		"status": "closed", "runId": entry.RunID, "ruleId": entry.RuleID,
		"entryEventId": entry.EventID, "exitEventId": exit.EventID,
		"entryFees": entry.Fees, "exitFees": exit.Fees, "entryCash": entryCash,
		"corporateActionCash": position.corporateCash, "adjustedEntryPrice": position.adjustedEntryPrice,
	})
	identity := sha256.Sum256([]byte(strings.Join([]string{backtestID, entry.RunID, entry.RuleID, entry.Symbol, entry.EventID, exit.EventID}, "|")))
	row := models.Trade{
		TradeID: "trade-" + hex.EncodeToString(identity[:16]), BacktestID: backtestID, StrategyVersion: strategyVersion,
		Sequence: sequence, Symbol: entry.Symbol, EntryAt: entry.EventAt.UTC(), ExitAt: &exitAt,
		EntryPrice: position.adjustedEntryPrice, ExitPrice: exit.Price, Quantity: position.quantity, Fees: roundedReplayValue(fees),
		GrossPnL: roundedReplayValue(gross), NetPnL: roundedReplayValue(net), ReturnPct: roundedReplayValue(net / entryCash * 100),
		ExitReason: firstNonEmptyReplay(exit.Reason, "unspecified"), SourceOrderEventIDs: string(sourceIDs), PayloadJSON: string(payload), FrozenAt: &frozenAt,
	}
	if err := sealSnapshotRecord(&row); err != nil {
		return models.Trade{}, err
	}
	return row, nil
}

func replayOpenToTrade(backtestID, strategyVersion string, sequence int, position replayPosition, frozenAt time.Time) (models.Trade, error) {
	entry := position.entry
	mark := position.markPrice
	if !finitePositive(mark) {
		mark = entry.Price
	}
	originalEntryNotional := entry.Price * entry.Quantity
	gross := mark*position.quantity + position.corporateCash - originalEntryNotional
	net := gross - entry.Fees
	entryCash := originalEntryNotional + entry.Fees
	sourceIDs, _ := json.Marshal(position.sourceEventIDs)
	payload, _ := json.Marshal(map[string]any{
		"status": "open", "runId": entry.RunID, "ruleId": entry.RuleID, "entryEventId": entry.EventID,
		"entryFees": entry.Fees, "exitFees": 0, "entryCash": entryCash, "markPrice": mark, "markAt": position.markAt.UTC(),
		"corporateActionCash": position.corporateCash, "adjustedEntryPrice": position.adjustedEntryPrice,
		"valuationMode": "event_price_or_entry_cost",
	})
	identity := sha256.Sum256([]byte(strings.Join([]string{backtestID, entry.RunID, entry.RuleID, entry.Symbol, entry.EventID, "open"}, "|")))
	row := models.Trade{
		TradeID: "trade-" + hex.EncodeToString(identity[:16]), BacktestID: backtestID, StrategyVersion: strategyVersion,
		Sequence: sequence, Symbol: entry.Symbol, EntryAt: entry.EventAt.UTC(), EntryPrice: position.adjustedEntryPrice, ExitPrice: mark,
		Quantity: position.quantity, Fees: roundedReplayValue(entry.Fees), GrossPnL: roundedReplayValue(gross), NetPnL: roundedReplayValue(net),
		ReturnPct: roundedReplayValue(net / entryCash * 100), ExitReason: "open_at_end", SourceOrderEventIDs: string(sourceIDs), PayloadJSON: string(payload), FrozenAt: &frozenAt,
	}
	if err := sealSnapshotRecord(&row); err != nil {
		return models.Trade{}, err
	}
	return row, nil
}

func calculatePortfolioReplayStats(trades []models.Trade, endingCash float64, events []models.OrderEvent) OrderEventReplayStats {
	cfg := v150.FixedStrategyV150Config()
	stats := OrderEventReplayStats{TradeCount: len(trades), InitialCash: cfg.PortfolioCash, EndingCash: endingCash, ProfitFactorText: "undefined"}
	var marks float64
	var closedReturnSum float64
	for _, trade := range trades {
		stats.GrossPnL += trade.GrossPnL
		stats.Fees += trade.Fees
		stats.EntryCash += replayTradeEntryCash(trade)
		if trade.ExitAt == nil {
			stats.OpenPositionCount++
			marks += trade.ExitPrice * trade.Quantity
			continue
		}
		stats.ClosedTradeCount++
		closedReturnSum += trade.ReturnPct
		switch {
		case trade.NetPnL > 0:
			stats.WinningTrades++
			stats.GrossProfit += trade.NetPnL
		case trade.NetPnL < 0:
			stats.LosingTrades++
			stats.GrossLoss += -trade.NetPnL
		default:
			stats.FlatTrades++
		}
	}
	stats.EndingEquity = endingCash + marks
	stats.NetPnL = stats.EndingEquity - cfg.PortfolioCash
	stats.PortfolioNetReturnPct = stats.NetPnL / cfg.PortfolioCash * 100
	stats.NetReturnPct = stats.PortfolioNetReturnPct
	if stats.ClosedTradeCount > 0 {
		stats.NetMeanReturnPct = closedReturnSum / float64(stats.ClosedTradeCount)
		stats.WinRatePct = float64(stats.WinningTrades) / float64(stats.ClosedTradeCount) * 100
	}
	if stats.GrossLoss > 0 {
		value := roundedReplayValue(stats.GrossProfit / stats.GrossLoss)
		stats.ProfitFactor, stats.ProfitFactorText = &value, fmt.Sprintf("%.8f", value)
	} else if stats.GrossProfit > 0 {
		stats.ProfitFactorText = "+Inf"
	}
	stats.Stress20EndingEquity = replayStressEndingEquity(events, cfg.StressSlippageBPS[0])
	stats.Stress20NetPnL = stats.Stress20EndingEquity - cfg.PortfolioCash
	stats.Stress20NetReturnPct = stats.Stress20NetPnL / cfg.PortfolioCash * 100
	stats.Stress50EndingEquity = replayStressEndingEquity(events, cfg.StressSlippageBPS[1])
	stats.Stress50NetPnL = stats.Stress50EndingEquity - cfg.PortfolioCash
	stats.Stress50NetReturnPct = stats.Stress50NetPnL / cfg.PortfolioCash * 100
	roundReplayStats(&stats)
	return stats
}

func replayStressEndingEquity(events []models.OrderEvent, bps float64) float64 {
	cfg := v150.FixedStrategyV150Config()
	cash := cfg.PortfolioCash
	positions := map[string]replayPosition{}
	for _, event := range events {
		key, kind := replayPositionKey(event), normalizedOrderEventType(event.EventType)
		if position, ok := positions[key]; ok && finitePositive(event.Price) && kind != "exit_fill" && kind != "fill" {
			position.markPrice, position.markAt = event.Price, event.EventAt
			positions[key] = position
		}
		switch kind {
		case "fill":
			raw := event.Price / (1 + cfg.BaseSlippageBPS/10_000)
			cost := v150.CalculateTradeCost(v150.SideBuy, replayMarket(event.Symbol), raw, int(event.Quantity), v150.SlippageScenario{Name: "stress", BPS: bps}, cfg)
			cash += cost.CashFlow
			positions[key] = replayPosition{entry: event, adjustedEntryPrice: event.Price, quantity: event.Quantity, markPrice: raw, markAt: event.EventAt}
		case "corporate_action":
			position, ok := positions[key]
			if !ok || event.AdjustmentFactor <= 0 || !integerShares(event.Quantity) || !finiteNonNegative(event.CashAmount) {
				return math.NaN()
			}
			position.adjustedEntryPrice *= event.AdjustmentFactor
			position.quantity = event.Quantity
			position.markPrice *= event.AdjustmentFactor
			cash += event.CashAmount
			positions[key] = position
		case "exit_fill":
			raw := event.Price / (1 - cfg.BaseSlippageBPS/10_000)
			cost := v150.CalculateTradeCost(v150.SideSell, replayMarket(event.Symbol), raw, int(event.Quantity), v150.SlippageScenario{Name: "stress", BPS: bps}, cfg)
			cash += cost.CashFlow
			delete(positions, key)
		}
	}
	for _, position := range positions {
		cash += position.markPrice * position.quantity
	}
	return roundedReplayValue(cash)
}

func roundReplayStats(stats *OrderEventReplayStats) {
	values := []*float64{&stats.InitialCash, &stats.EndingCash, &stats.EndingEquity, &stats.GrossPnL, &stats.Fees, &stats.NetPnL, &stats.EntryCash, &stats.GrossProfit, &stats.GrossLoss, &stats.NetReturnPct, &stats.PortfolioNetReturnPct, &stats.NetMeanReturnPct, &stats.WinRatePct, &stats.Stress20EndingEquity, &stats.Stress20NetPnL, &stats.Stress20NetReturnPct, &stats.Stress50EndingEquity, &stats.Stress50NetPnL, &stats.Stress50NetReturnPct}
	for _, value := range values {
		*value = roundedReplayValue(*value)
	}
}

func replayEntryFee(trade models.Trade) float64 {
	var payload struct {
		EntryFees float64 `json:"entryFees"`
	}
	if json.Unmarshal([]byte(trade.PayloadJSON), &payload) == nil && payload.EntryFees >= 0 {
		return payload.EntryFees
	}
	return 0
}

func replayTradeEntryCash(trade models.Trade) float64 {
	var payload struct {
		EntryCash float64 `json:"entryCash"`
	}
	if json.Unmarshal([]byte(trade.PayloadJSON), &payload) == nil && payload.EntryCash > 0 {
		return payload.EntryCash
	}
	return trade.EntryPrice*trade.Quantity + replayEntryFee(trade)
}

func replayResultHash(backtestID, strategyVersion string, trades []models.Trade, stats OrderEventReplayStats) string {
	hashes := make([]string, len(trades))
	for i := range trades {
		hashes[i] = trades[i].SnapshotHash
	}
	payload := struct {
		BacktestID, StrategyVersion string
		TradeHashes                 []string
		Stats                       OrderEventReplayStats
	}{backtestID, strategyVersion, hashes, stats}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func replayEventTradeDayIndex(event models.OrderEvent) int {
	var payload struct {
		TradeDayIndex int `json:"tradeDayIndex"`
	}
	if json.Unmarshal([]byte(event.PayloadJSON), &payload) == nil && payload.TradeDayIndex > 0 {
		return payload.TradeDayIndex
	}
	// Compatibility fallback for early preview ledgers. Released V1.5 events
	// persist the exact strategy trade-day index above; the fallback only needs
	// a monotonic weekday ordinal to fail closed on obviously short cooldowns.
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	day := event.EventAt.In(zone)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, zone)
	epoch := time.Date(2000, 1, 3, 0, 0, 0, 0, zone)
	if day.Before(epoch) {
		return 0
	}
	weeks := int(day.Sub(epoch).Hours()/24) / 7
	remaining := int(day.Sub(epoch).Hours()/24) % 7
	index := weeks * 5
	for offset := 0; offset <= remaining; offset++ {
		probe := epoch.AddDate(0, 0, offset)
		if probe.Weekday() != time.Saturday && probe.Weekday() != time.Sunday {
			index++
		}
	}
	return index
}

func replayIsStopReason(raw string) bool {
	text := strings.ToLower(strings.TrimSpace(raw))
	return strings.Contains(text, "stop") || strings.Contains(text, "止损")
}

func cnDateText(at time.Time) string {
	return at.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format(time.DateOnly)
}
func normalizedOrderEventType(raw string) string { return strings.ToLower(strings.TrimSpace(raw)) }
func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func finiteNumber(value float64) bool      { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finiteNonNegative(value float64) bool { return value >= 0 && finiteNumber(value) }
func nearlyEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
func roundedReplayValue(value float64) float64 { return math.Round(value*1e8) / 1e8 }
func firstNonEmptyReplay(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
