package data

import (
	"time"

	"go-stock/backend/instruments"
)

const (
	ChartPeriod1Minute  = "1m"
	ChartPeriod5Minute  = "5m"
	ChartPeriod15Minute = "15m"
	ChartPeriod30Minute = "30m"
	ChartPeriod60Minute = "60m"
	ChartPeriodDay      = "day"
	ChartPeriodWeek     = "week"
	ChartPeriodMonth    = "month"
	ChartPeriodQuarter  = "quarter"
	ChartPeriodYear     = "year"

	ChartAdjustmentNone = "none"
	ChartAdjustmentQFQ  = "qfq"
	ChartAdjustmentHFQ  = "hfq"
)

type ChartRequest struct {
	Instrument instruments.InstrumentID
	Period     string
	Adjustment string
	From       time.Time
	To         time.Time
	Limit      int
}

type ChartBar struct {
	At     time.Time `json:"time"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume float64   `json:"volume"`
	Amount float64   `json:"amount"`
	Source string    `json:"source"`
}

type ChartMissingInterval struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Reason string    `json:"reason"`
}

type ChartData struct {
	Instrument       instruments.InstrumentID `json:"instrument"`
	Period           string                   `json:"period"`
	Adjustment       string                   `json:"adjustment"`
	Timezone         string                   `json:"timezone"`
	RangeFrom        time.Time                `json:"rangeFrom"`
	RangeTo          time.Time                `json:"rangeTo"`
	Bars             []ChartBar               `json:"bars"`
	MissingIntervals []ChartMissingInterval   `json:"missingIntervals"`
}

type ChartDrawingPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

type ChartDrawing struct {
	ID        string              `json:"id"`
	Type      string              `json:"type"`
	Points    []ChartDrawingPoint `json:"points"`
	Style     map[string]any      `json:"style,omitempty"`
	DeletedAt *time.Time          `json:"deletedAt,omitempty"`
	CreatedAt *time.Time          `json:"createdAt,omitempty"`
	UpdatedAt *time.Time          `json:"updatedAt,omitempty"`
}

type ChartDrawingDocument struct {
	Instrument instruments.InstrumentID `json:"instrument"`
	Period     string                   `json:"period"`
	Adjustment string                   `json:"adjustment"`
	Revision   int64                    `json:"revision"`
	Drawings   []ChartDrawing           `json:"drawings"`
	DeletedAt  *time.Time               `json:"deletedAt,omitempty"`
	UpdatedAt  time.Time                `json:"updatedAt"`
}

type ChartDrawingScope struct {
	ScopeType string
	ScopeID   string
	Request   ChartRequest
}
