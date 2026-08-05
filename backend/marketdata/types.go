// Package marketdata defines provider-neutral, point-in-time market data.
package marketdata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidObservation     = errors.New("invalid market-data observation")
	ErrObservationUnavailable = errors.New("market-data observation unavailable")
)

type Adjustment string

const (
	AdjustmentUnknown Adjustment = ""
	AdjustmentNone    Adjustment = "none"
	AdjustmentForward Adjustment = "forward"
)

type DailyBar struct {
	Symbol      string     `json:"symbol"`
	TradeDate   time.Time  `json:"tradeDate"`
	Open        float64    `json:"open"`
	High        float64    `json:"high"`
	Low         float64    `json:"low"`
	Close       float64    `json:"close"`
	Volume      float64    `json:"volume"`
	Amount      float64    `json:"amount"`
	Adjustment  Adjustment `json:"adjustment"`
	Completed   bool       `json:"completed"`
	Source      string     `json:"source"`
	SourceAt    time.Time  `json:"sourceAt"`
	AvailableAt time.Time  `json:"availableAt"`
}

type MinuteBar struct {
	Symbol              string    `json:"symbol"`
	Index               int       `json:"index"`
	TradeDayIndex       int       `json:"tradeDayIndex"`
	IntervalMinutes     int       `json:"intervalMinutes"`
	Start               time.Time `json:"start"`
	End                 time.Time `json:"end"`
	Open                float64   `json:"open"`
	High                float64   `json:"high"`
	Low                 float64   `json:"low"`
	Close               float64   `json:"close"`
	Volume              float64   `json:"volume"`
	Amount              float64   `json:"amount"`
	VolumeRatioSameSlot float64   `json:"volumeRatioSameSlot"`
	Completed           bool      `json:"completed"`
	Suspended           bool      `json:"suspended"`
	LimitUpLocked       bool      `json:"limitUpLocked"`
	LimitDownLocked     bool      `json:"limitDownLocked"`
	Source              string    `json:"source"`
	SourceAt            time.Time `json:"sourceAt"`
	AvailableAt         time.Time `json:"availableAt"`
}

type Quote struct {
	Symbol        string    `json:"symbol"`
	Name          string    `json:"name"`
	Price         float64   `json:"price"`
	Open          float64   `json:"open"`
	PreviousClose float64   `json:"previousClose"`
	High          float64   `json:"high"`
	Low           float64   `json:"low"`
	Volume        float64   `json:"volume"`
	Amount        float64   `json:"amount"`
	ObservedAt    time.Time `json:"observedAt"`
	Source        string    `json:"source"`
	SourceAt      time.Time `json:"sourceAt"`
	AvailableAt   time.Time `json:"availableAt"`
}

type TradingStatus string

const (
	TradingStatusUnknown   TradingStatus = "unknown"
	TradingStatusTradable  TradingStatus = "tradable"
	TradingStatusSuspended TradingStatus = "suspended"
	TradingStatusDelisted  TradingStatus = "delisted"
)

type SecurityState struct {
	Symbol        string        `json:"symbol"`
	Name          string        `json:"name"`
	Market        string        `json:"market"`
	Exchange      string        `json:"exchange"`
	Board         string        `json:"board"`
	Sector        string        `json:"sector"`
	Industry      string        `json:"industry"`
	Currency      string        `json:"currency"`
	Status        TradingStatus `json:"status"`
	ST            bool          `json:"st"`
	ListedAt      *time.Time    `json:"listedAt,omitempty"`
	DelistedAt    *time.Time    `json:"delistedAt,omitempty"`
	EffectiveFrom time.Time     `json:"effectiveFrom"`
	EffectiveTo   *time.Time    `json:"effectiveTo,omitempty"`
	Source        string        `json:"source"`
	SourceAt      time.Time     `json:"sourceAt"`
	AvailableAt   time.Time     `json:"availableAt"`
}

func (s SecurityState) Tradable() bool {
	return s.Status == TradingStatusTradable && !s.ST
}

type DailyBarsRequest struct {
	Symbol string
	Start  time.Time
	End    time.Time
	AsOf   time.Time
}

type MinuteBarsRequest struct {
	Symbol string
	Start  time.Time
	End    time.Time
	AsOf   time.Time
}

type DailyBarReader interface {
	DailyBars(context.Context, DailyBarsRequest) ([]DailyBar, error)
}

type MinuteBarReader interface {
	MinuteBars(context.Context, MinuteBarsRequest) ([]MinuteBar, error)
}

type QuoteReader interface {
	Quote(context.Context, string, time.Time) (Quote, error)
}

type SecurityStateReader interface {
	SecurityState(context.Context, string, time.Time) (SecurityState, error)
}

type Reader interface {
	DailyBarReader
	MinuteBarReader
	QuoteReader
	SecurityStateReader
}

func ValidateTimeline(sourceAt, availableAt, asOf time.Time) error {
	if sourceAt.IsZero() || availableAt.IsZero() || asOf.IsZero() {
		return fmt.Errorf("%w: sourceAt, availableAt and asOf are required", ErrInvalidObservation)
	}
	if sourceAt.After(availableAt) || availableAt.After(asOf) {
		return fmt.Errorf("%w: require sourceAt <= availableAt <= asOf", ErrInvalidObservation)
	}
	return nil
}

func ValidateSymbol(symbol string) error {
	if strings.TrimSpace(symbol) == "" {
		return fmt.Errorf("%w: symbol is required", ErrInvalidObservation)
	}
	return nil
}
