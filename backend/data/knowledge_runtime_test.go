package data

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-stock/backend/research"
	"go-stock/backend/research2"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeReportLoaderReadsExistingResearch1AndResearch2Reports(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:knowledge-report-loader?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&research.AnalysisRun{}, &research2.AnalysisRun{}, &research2.EmailDelivery{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 9, 58, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	r1 := research.AnalysisRun{RunID: "r1-report", ScheduledFor: now, StartedAt: now, Status: "success", FinalReport: "# R1 最终报告"}
	if err = research.NewRepository(database).CreateAnalysis(context.Background(), &r1); err != nil {
		t.Fatal(err)
	}
	r2 := research2.AnalysisRun{RunID: "r2-report", TradingDate: "2026-08-28", ScheduledFor: now, StartedAt: now, EvidenceCutoffAt: now, Status: "success", SourceStatusJSON: "[]", ModelAttemptLogJSON: "[]", ReportMarkdown: "# R2 最终报告"}
	if err = research2.NewRepository(database).CreateRun(context.Background(), &r2); err != nil {
		t.Fatal(err)
	}
	loader := knowledgeReportLoader{database: database}
	for _, fixture := range []struct{ owner, id, marker string }{{"research1", r1.RunID, "R1 最终报告"}, {"research2", r2.RunID, "R2 最终报告"}} {
		report, loadErr := loader.LoadResearchReport(context.Background(), fixture.owner, fixture.id)
		if loadErr != nil || !strings.Contains(report.Content, fixture.marker) || strings.TrimSpace(report.Title) == "" {
			t.Fatalf("owner=%s report=%+v err=%v", fixture.owner, report, loadErr)
		}
	}
}
