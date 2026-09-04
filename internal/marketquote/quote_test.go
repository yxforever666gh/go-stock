package marketquote

import (
	"encoding/json"
	"testing"
	"time"
)

func TestQuoteJSONShape(t *testing.T) {
	quote := Quote{
		Code: "sh600000", Name: "浦发银行", Market: "SH", Price: 10.1, PreviousClose: 10,
		Volume: 1200, Amount: 12120, At: time.Date(2026, 9, 4, 9, 30, 0, 0, time.UTC), LimitUp: true,
	}
	encoded, err := json.Marshal(quote)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"code":"sh600000","name":"浦发银行","market":"SH","price":10.1,"previousClose":10,"volume":1200,"amount":12120,"at":"2026-09-04T09:30:00Z","suspended":false,"limitUp":true,"limitDown":false}`
	if string(encoded) != want {
		t.Fatalf("quote JSON=%s", encoded)
	}
}
