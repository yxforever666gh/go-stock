package main

import (
	"context"
	"testing"
	"time"

	"go-stock/backend/research"

	"github.com/robfig/cron/v3"
)

func TestNextCapitalDeploymentWindow(t *testing.T) {
	service := research.NewService(nil, nil, nil, research.WeekdayCalendar{})
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	tests := []struct {
		name      string
		requested time.Time
		want      string
	}{
		{name: "pre open", requested: time.Date(2026, 8, 17, 8, 20, 0, 0, location), want: "2026-08-17 09:35"},
		{name: "morning", requested: time.Date(2026, 8, 17, 10, 20, 0, 0, location), want: "2026-08-17 10:20"},
		{name: "lunch", requested: time.Date(2026, 8, 17, 12, 20, 0, 0, location), want: "2026-08-17 13:00"},
		{name: "afternoon", requested: time.Date(2026, 8, 17, 14, 20, 0, 0, location), want: "2026-08-17 14:20"},
		{name: "one second after cutoff", requested: time.Date(2026, 8, 17, 14, 25, 1, 0, location), want: "2026-08-18 09:35"},
		{name: "after cutoff", requested: time.Date(2026, 8, 17, 14, 26, 0, 0, location), want: "2026-08-18 09:35"},
		{name: "weekend", requested: time.Date(2026, 8, 22, 10, 0, 0, 0, location), want: "2026-08-24 09:35"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nextCapitalDeploymentWindow(context.Background(), service, test.requested)
			if err != nil {
				t.Fatal(err)
			}
			if formatted := got.Format("2006-01-02 15:04"); formatted != test.want {
				t.Fatalf("window=%s want=%s", formatted, test.want)
			}
		})
	}
}

func TestCapitalDeploymentWindowCutoff(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	if !research.IsCapitalDeploymentAnalysisWindow(time.Date(2026, 8, 17, 11, 30, 0, 0, location)) {
		t.Fatal("11:30:00 must still allow a new claim")
	}
	if research.IsCapitalDeploymentAnalysisWindow(time.Date(2026, 8, 17, 11, 30, 1, 0, location)) {
		t.Fatal("the lunch break must defer a new claim")
	}
	if !research.IsCapitalDeploymentAnalysisWindow(time.Date(2026, 8, 17, 14, 25, 0, 0, location)) {
		t.Fatal("14:25 must still allow a new claim")
	}
	if research.IsCapitalDeploymentAnalysisWindow(time.Date(2026, 8, 17, 14, 25, 1, 0, location)) {
		t.Fatal("after 14:25 must defer to the next trading day")
	}
}

func TestResearchSnapshotCronRetriesEveryFiveMinutesAfter1505(t *testing.T) {
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(researchSnapshotCronSpec)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 4, 15, 4, 59, 0, time.Local)
	first := schedule.Next(base)
	second := schedule.Next(first)
	if first.Hour() != 15 || first.Minute() != 5 || first.Second() != 0 || second.Minute() != 10 {
		t.Fatalf("first=%s second=%s", first, second)
	}
	afterWindow := time.Date(2026, 9, 4, 15, 55, 0, 0, time.Local)
	next := schedule.Next(afterWindow)
	if next.Day() != 5 || next.Hour() != 15 || next.Minute() != 5 {
		t.Fatalf("next=%s", next)
	}
}
