package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"go-stock/backend/research"
)

type fakeResearchRepository struct {
	running bool
	err     error
}

func (f fakeResearchRepository) HasRunningAnalysis(context.Context) (bool, error) {
	return f.running, f.err
}

type fakeResearchRunner struct {
	run   research.AnalysisRun
	err   error
	calls int
}

func (f *fakeResearchRunner) Run(context.Context, research.AnalysisRequest) (research.AnalysisRun, error) {
	f.calls++
	return f.run, f.err
}

func TestExecuteResearchOnceRejectsConcurrentRun(t *testing.T) {
	runner := &fakeResearchRunner{}
	_, err := executeResearchOnce(context.Background(), fakeResearchRepository{running: true}, runner, research.AnalysisRequest{})
	if err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("error=%v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls=%d, want 0", runner.calls)
	}
}

func TestExecuteResearchOnceRunsSingleAnalysis(t *testing.T) {
	want := research.AnalysisRun{RunID: "run-1", Status: "no_recommendation", StartedAt: time.Now()}
	runner := &fakeResearchRunner{run: want}
	got, err := executeResearchOnce(context.Background(), fakeResearchRepository{}, runner, research.AnalysisRequest{})
	if err != nil || got.RunID != want.RunID || runner.calls != 1 {
		t.Fatalf("got=%+v err=%v calls=%d", got, err, runner.calls)
	}
}

func TestResearchIsRecognizedAsCLICommand(t *testing.T) {
	if !IsCommand("research") || !HasCommand([]string{"--db-path", "stock.db", "research", "run-once"}) {
		t.Fatal("research command was not recognized")
	}
}

func TestResearchRepairCommandsAreRetired(t *testing.T) {
	for _, command := range []string{"repair-missed-cash", "repair-xd-sell", "repair-post-sell-buy"} {
		t.Run(command, func(t *testing.T) {
			err := runResearch([]string{command}, GlobalOptions{}, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "未知 research 子命令") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
