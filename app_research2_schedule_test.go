package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go-stock/backend/ai"
	"go-stock/backend/models"
	"go-stock/backend/research2"
	"go-stock/backend/research2app"
	"go-stock/backend/researchconfig"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestResearch2RecoveryWindowIsHalfOpen(t *testing.T) {
	location := research2Location()
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "old start", at: time.Date(2026, 9, 3, 9, 50, 0, 0, location), want: false},
		{name: "before", at: time.Date(2026, 9, 3, 9, 54, 59, 0, location), want: false},
		{name: "start", at: time.Date(2026, 9, 3, 9, 55, 0, 0, location), want: true},
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

func TestResearch2AnalysisCronAndScheduledRootUse0955(t *testing.T) {
	if research2AnalysisCronSpec != "0 55 9 * * 1-5" {
		t.Fatalf("analysis cron=%q want 09:55 on weekdays", research2AnalysisCronSpec)
	}
	location := research2Location()
	root := research2ScheduledRoot(time.Date(2026, 9, 3, 12, 34, 56, 0, location))
	want := time.Date(2026, 9, 3, 9, 55, 0, 0, location)
	if !root.Equal(want) {
		t.Fatalf("scheduled root=%s want=%s", root, want)
	}
}

func TestResearch2RecoveryOutsideWindowAndWeekendDoNotCreateRuntime(t *testing.T) {
	location := research2Location()
	created := 0
	app := &App{
		ctx: context.Background(),
		research2Factory: func(*models.SettingConfig) (*research2app.Runtime, error) {
			created++
			return nil, nil
		},
	}
	for _, at := range []time.Time{
		time.Date(2026, 9, 3, 9, 50, 0, 0, location),
		time.Date(2026, 9, 3, 9, 54, 59, 0, location),
		time.Date(2026, 9, 3, 13, 0, 0, 0, location),
		time.Date(2026, 9, 5, 10, 0, 0, 0, location),
	} {
		app.recoverResearch2Schedule(at)
	}
	if created != 0 {
		t.Fatalf("recovery created runtime %d times outside an eligible trading window", created)
	}
}

type research2ResumeEvidence struct{ calls int }

func (e *research2ResumeEvidence) Collect(_ context.Context, at time.Time) (research2.Evidence, error) {
	e.calls++
	return research2.Evidence{CutoffAt: at, Prompt: "{}", SourceStatusJSON: "[]"}, nil
}

type research2ResumeAI struct{ calls int }

func (a *research2ResumeAI) Complete(context.Context, ai.CompletionRequest) (ai.CompletionResult, error) {
	a.calls++
	return ai.CompletionResult{Content: `{"tradingDay":true,"conclusion":"fixture complete","recommendations":[]}`}, nil
}

type research2ResumeCalendar struct{}

func (research2ResumeCalendar) IsTradingDay(context.Context, time.Time) (bool, error) {
	return true, nil
}

type research2ResumeMarket struct{}

func (research2ResumeMarket) PriceAt(context.Context, string, time.Time, bool) (research2.PriceSnapshot, error) {
	panic("resume fixture must not trade")
}
func (research2ResumeMarket) Metrics(context.Context, research2.Recommendation) (research2.MetricSnapshot, error) {
	panic("resume fixture must not finalize metrics")
}

func TestResearch2ResumeRetriesFailedRunWithoutActiveChain(t *testing.T) {
	for _, tc := range []struct {
		name, chainStatus, runStatus string
		hour, minute                 int
		enabled, wantRetry           bool
	}{
		{"pre-chain failure", "", "failed", 10, 0, true, true},
		{"legacy failed chain", "failed", "failed", 10, 0, true, true},
		{"running chain failed attempt", "running", "failed", 10, 0, true, true},
		{"completed chain", "completed", "failed", 10, 0, true, false},
		{"disabled chain", "disabled", "failed", 10, 0, true, false},
		{"cutoff chain", "cutoff", "failed", 10, 0, true, false},
		{"exhausted chain", "exhausted", "failed", 10, 0, true, false},
		{"nonfailure without chain", "", "no_recommendation", 10, 0, true, false},
		{"nonfailure with failed chain", "failed", "success", 10, 0, true, false},
		{"before window", "", "failed", 9, 54, true, false},
		{"at cutoff", "", "failed", 13, 0, true, false},
		{"automatic strategy disabled", "", "failed", 10, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "research2-resume.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := database.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = connection.Close() })
			if err := database.AutoMigrate(&researchconfig.Record{}, &models.AIConfig{}, &research2.AnalysisRun{}, &research2.ExecutionChain{}, &research2.Recommendation{}, &research2.Trade{}, &research2.Account{}, &research2.AccountSnapshot{}); err != nil {
				t.Fatal(err)
			}
			raw, err := researchconfig.ConfigJSON(researchconfig.Research2, &models.SettingConfig{Settings: &models.Settings{Research2AutoEnabled: tc.enabled, BrowserPath: "fixture-browser"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := database.Create(&researchconfig.Record{Center: researchconfig.Research2, Revision: 1, ConfigJSON: string(raw)}).Error; err != nil {
				t.Fatal(err)
			}
			repository := research2.NewRepository(database)
			if err := repository.EnsureAccount(context.Background()); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 7, tc.hour, tc.minute, 0, 0, research2Location())
			started := research2ScheduledRoot(now)
			old := research2.AnalysisRun{RunID: "original-failure", TradingDate: "2026-09-07", AttemptNo: 1, ScheduledFor: started, StartedAt: started, EvidenceCutoffAt: started, Status: tc.runStatus, FailureReason: "original audit/calendar failure", StrategyVersion: "research2-trailing5-v9", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]"}
			if tc.chainStatus != "" {
				chain, err := repository.EnsureExecutionChain(context.Background(), old.TradingDate, started, started)
				if err != nil {
					t.Fatal(err)
				}
				old.ChainID = chain.ChainID
				if err := database.Model(&research2.ExecutionChain{}).Where("chain_id = ?", chain.ChainID).Updates(map[string]any{"status": tc.chainStatus, "root_run_id": old.RunID, "latest_run_id": old.RunID}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := repository.CreateRun(context.Background(), &old); err != nil {
				t.Fatal(err)
			}
			collector, model := &research2ResumeEvidence{}, &research2ResumeAI{}
			runner := research2.NewRunner(repository, model, collector, research2ResumeCalendar{})
			runner.ConfigureReplayClock(func() time.Time { return now }, nil)
			store := researchconfig.New(database)
			settings, err := store.Load(context.Background(), researchconfig.Research2)
			if err != nil {
				t.Fatal(err)
			}
			app := &App{ctx: context.Background(), researchConfigStore: store, researchDatabase: database, research2Settings: settings.Settings, research2Runtime: &research2app.Runtime{Repository: repository, Runner: runner, Trading: research2.NewTradingService(repository, research2ResumeMarket{}, research2ResumeCalendar{})}}
			app.resumeResearch2ExecutionChain(now)
			latest, exists, err := repository.RunForDate(context.Background(), old.TradingDate)
			if err != nil || !exists {
				t.Fatalf("latest=%+v err=%v", latest, err)
			}
			wantCalls, wantAttempt := 0, 1
			if tc.wantRetry {
				wantCalls, wantAttempt = 1, 2
			}
			if collector.calls != wantCalls || model.calls != wantCalls || latest.AttemptNo != wantAttempt {
				t.Fatalf("collector=%d AI=%d attempt=%d want=%d/%d", collector.calls, model.calls, latest.AttemptNo, wantCalls, wantAttempt)
			}
			original, err := repository.AnalysisRunByID(context.Background(), old.RunID)
			if err != nil || original.Status != old.Status || original.FailureReason != old.FailureReason {
				t.Fatalf("original audit was changed: %+v err=%v", original, err)
			}
			if tc.chainStatus != "" && !tc.wantRetry {
				chain, err := repository.ExecutionChain(context.Background(), old.ChainID)
				if err != nil || chain.Status != tc.chainStatus {
					t.Fatalf("terminal chain changed: %+v err=%v", chain, err)
				}
			}
		})
	}
}
