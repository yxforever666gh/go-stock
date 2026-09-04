package data

import (
	"strings"
	"testing"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/internal/researchevidence"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func research2EvidenceTestRepository(t *testing.T) *marketdata.Repository {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&marketdata.EvidenceBatch{}, &marketdata.EvidenceItem{}); err != nil {
		t.Fatal(err)
	}
	return marketdata.NewRepository(database)
}

func TestValidateResearch2CandidateCutoff(t *testing.T) {
	cutoff := time.Date(2026, 8, 28, 9, 55, 0, 0, shanghaiDataLocation())
	if err := validateResearch2CandidateCutoff(true, cutoff.Add(time.Nanosecond), cutoff); err == nil {
		t.Fatal("experimental mode accepted an after-cutoff candidate snapshot")
	}
	if err := validateResearch2CandidateCutoff(true, cutoff, cutoff); err != nil {
		t.Fatalf("cutoff-equal candidate rejected: %v", err)
	}
	if err := validateResearch2CandidateCutoff(false, cutoff.Add(time.Minute), cutoff); err != nil {
		t.Fatalf("legacy mode changed: %v", err)
	}
}

func TestResearchEvidenceSourceIdentityAndSummary(t *testing.T) {
	document := researchevidence.SourceDocument{SourceID: "S001", SourceName: "东方财富", Category: "market", Content: "候选输入与关键行情字段"}
	used := map[string]int{}
	if got := uniqueResearchEvidenceSourceID(document, 0, used); got != "S001" {
		t.Fatalf("source id not preserved: %q", got)
	}
	if got := uniqueResearchEvidenceSourceID(document, 1, used); got == "S001" || !strings.HasPrefix(got, "S001-") {
		t.Fatalf("duplicate source id not disambiguated: %q", got)
	}
	summary := researchEvidenceDocumentSummary(document)
	if !strings.Contains(summary, "sourceId=S001") || !strings.Contains(summary, "候选输入") {
		t.Fatalf("summary is not traceable or content-bearing: %q", summary)
	}
}
