package researchapp

import (
	"context"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/internal/researchevidence"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type sourceFixture struct{ name string }

func (*sourceFixture) CollectMarket(context.Context, time.Time) ([]researchevidence.SourceDocument, error) {
	return nil, nil
}
func (*sourceFixture) CollectSectors(context.Context, time.Time) ([]researchevidence.SourceDocument, error) {
	return nil, nil
}
func (*sourceFixture) CollectStocks(context.Context, time.Time, []researchevidence.StockCandidate) ([]researchevidence.SourceDocument, error) {
	return nil, nil
}

func TestActiveSourcesKeepsBaseUnlessExperimentalEnabled(t *testing.T) {
	base, experimental := &sourceFixture{name: "base"}, &sourceFixture{name: "experimental"}
	dependencies := Dependencies{Sources: base, ExperimentalSources: experimental}
	if got := activeSources(dependencies, false); got != base {
		t.Fatal("non-experimental runtime did not keep the base source collector")
	}
	if got := activeSources(dependencies, true); got != experimental {
		t.Fatal("experimental runtime did not select the enriched source collector")
	}
}

func TestNewRuntimeOwnsResearchServiceGraph(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&research.SimulatedAccount{}); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(database, Dependencies{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Repository == nil || runtime.Service == nil || runtime.Runner == nil {
		t.Fatalf("incomplete Research1 runtime: %+v", runtime)
	}
}

func TestNewRuntimeRejectsMissingStorage(t *testing.T) {
	if _, err := NewRuntime(nil, Dependencies{}, Options{}); err == nil {
		t.Fatal("missing main storage was accepted")
	}
}
