package data

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLimitResearch2TextKeepsUTF8Valid(t *testing.T) {
	result := limitResearch2Text(strings.Repeat("中", 10), 5)
	if !utf8.ValidString(result) || !strings.HasSuffix(result, "…") {
		t.Fatalf("truncated evidence must remain valid UTF-8: %q", result)
	}
}

func TestListedForResearch2SessionsRequiresTenOpenDays(t *testing.T) {
	loc := shanghaiDataLocation()
	asOf := time.Date(2026, 8, 28, 10, 0, 0, 0, loc)
	weekdays := func(day time.Time) (bool, error) {
		return day.Weekday() != time.Saturday && day.Weekday() != time.Sunday, nil
	}
	if !listedForResearch2Sessions(20260817, asOf, 10, weekdays) {
		t.Fatal("ten completed weekday sessions should be eligible")
	}
	if listedForResearch2Sessions(20260820, asOf, 10, weekdays) {
		t.Fatal("a stock with fewer than ten sessions must be excluded")
	}
}
