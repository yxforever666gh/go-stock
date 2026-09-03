package main

import (
	"context"
	"testing"
	"time"

	"go-stock/backend/data"
)

func TestResearch2RecoveryWindowIsHalfOpen(t *testing.T) {
	location := research2Location()
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "before", at: time.Date(2026, 9, 3, 9, 49, 59, 0, location), want: false},
		{name: "start", at: time.Date(2026, 9, 3, 9, 50, 0, 0, location), want: true},
		{name: "during", at: time.Date(2026, 9, 3, 10, 14, 0, 0, location), want: true},
		{name: "morning close", at: time.Date(2026, 9, 3, 11, 30, 0, 0, location), want: true},
		{name: "last second", at: time.Date(2026, 9, 3, 12, 59, 59, 0, location), want: true},
		{name: "end", at: time.Date(2026, 9, 3, 13, 0, 0, 0, location), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := withinResearch2RecoveryWindow(test.at); got != test.want {
				t.Fatalf("withinResearch2RecoveryWindow(%s)=%t want=%t", test.at, got, test.want)
			}
		})
	}
}

func TestResearch2RecoveryOutsideWindowAndWeekendDoNotCreateRuntime(t *testing.T) {
	location := research2Location()
	created := 0
	app := &App{
		ctx: context.Background(),
		research2Factory: func(int) (*data.Research2Runtime, error) {
			created++
			return nil, nil
		},
	}
	for _, at := range []time.Time{
		time.Date(2026, 9, 3, 9, 49, 59, 0, location),
		time.Date(2026, 9, 3, 13, 0, 0, 0, location),
		time.Date(2026, 9, 5, 10, 0, 0, 0, location),
	} {
		app.recoverResearch2Schedule(1, at)
	}
	if created != 0 {
		t.Fatalf("recovery created runtime %d times outside an eligible trading window", created)
	}
}
