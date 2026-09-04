package marketquote

import "time"

type Quote struct {
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Market        string    `json:"market"`
	Price         float64   `json:"price"`
	PreviousClose float64   `json:"previousClose"`
	Volume        float64   `json:"volume"`
	Amount        float64   `json:"amount"`
	At            time.Time `json:"at"`
	Suspended     bool      `json:"suspended"`
	LimitUp       bool      `json:"limitUp"`
	LimitDown     bool      `json:"limitDown"`
}
