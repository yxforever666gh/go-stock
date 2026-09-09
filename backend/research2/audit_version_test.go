package research2

import (
	"context"
	"reflect"
	"testing"
	"time"

	aicontract "go-stock/backend/ai"
	"go-stock/backend/researchaudit"
)

func TestRunnerAuditVersionsCoexistWithLegacyAndReuseContent(t *testing.T) {
	const valid = `{"tradingDay":true,"conclusion":"空仓","recommendations":[]}`
	for _, scenario := range []struct {
		name     string
		client   func(*time.Time) aicontract.AIClient
		payloads int
		phases   int
	}{
		{"normal", func(_ *time.Time) aicontract.AIClient { return &sequenceAI{responses: []string{valid}} }, 1, 1},
		{"structure_repair", func(_ *time.Time) aicontract.AIClient { return &sequenceAI{responses: []string{"invalid JSON", valid}} }, 2, 2},
		{"provider_retry", func(_ *time.Time) aicontract.AIClient { return multiAttemptAI{} }, 2, 1},
		{"no_provider_record", func(now *time.Time) aicontract.AIClient {
			return &advancingAI{current: now, advance: *now, response: valid}
		}, 1, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			repository := research2TestRepository(t)
			if err := repository.DB().AutoMigrate(&researchaudit.PromptVersion{}, &researchaudit.Payload{}, &researchaudit.RunState{}); err != nil {
				t.Fatal(err)
			}
			auditRepository := researchaudit.NewRepository(repository.DB())
			recorder := researchaudit.NewRecorder(auditRepository)
			if err := recorder.Begin(ctx, researchaudit.OwnerResearch2, "legacy-run"); err != nil {
				t.Fatal(err)
			}
			for index, phase := range []string{"research2_overnight_strength", "research2_overnight_strength_repair"} {
				prepared, err := recorder.Prepare(ctx, researchaudit.CallInput{OwnerType: researchaudit.OwnerResearch2, OwnerID: "legacy-run", Phase: phase, CallSequence: index + 1, Template: "historical prompt before score explanations", TemplateVersion: "research2-trailing5-v9", Prompt: "historical rendered prompt"})
				if err != nil {
					t.Fatal(err)
				}
				if err = recorder.Record(ctx, prepared, researchaudit.CallResult{RawResponse: "historical response"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := recorder.Complete(ctx, researchaudit.OwnerResearch2, "legacy-run"); err != nil {
				t.Fatal(err)
			}
			var oldVersions []researchaudit.PromptVersion
			if err := repository.DB().Order("phase").Find(&oldVersions).Error; err != nil {
				t.Fatal(err)
			}
			oldPayloads, err := auditRepository.ListPayloads(ctx, researchaudit.OwnerResearch2, "legacy-run")
			if err != nil {
				t.Fatal(err)
			}
			versionByPhase := map[string]string{}
			for day := 0; day < 2; day++ {
				now := time.Date(2026, 9, 8+day, 9, 57, 0, 0, shanghai())
				runner := NewRunner(repository, scenario.client(&now), fixedEvidence{value: Evidence{Prompt: "fixture evidence", SourceStatusJSON: "[]"}}, testCalendar{})
				runner.ConfigureAudit(recorder)
				runner.ConfigureReplayClock(func() time.Time { return now }, func(context.Context, time.Time) error { return nil })
				run, err := runner.Run(ctx, now)
				if err != nil {
					t.Fatal(err)
				}
				view, err := recorder.Audit(ctx, researchaudit.OwnerResearch2, run.RunID)
				if err != nil {
					t.Fatal(err)
				}
				if run.Status == "failed" || view.Status != researchaudit.StatusComplete || len(view.Payloads) != scenario.payloads {
					t.Fatalf("run=%+v audit=%+v", run, view)
				}
				for _, payload := range view.Payloads {
					if payload.PromptVersionID == nil {
						t.Fatal("missing template version")
					}
					var version researchaudit.PromptVersion
					if err := repository.DB().First(&version, "prompt_version_id = ?", *payload.PromptVersionID).Error; err != nil {
						t.Fatal(err)
					}
					if version.Version == "research2-trailing5-v9" || version.TemplateSHA256 == oldVersions[0].TemplateSHA256 {
						t.Fatal("reused legacy template")
					}
					if previous, exists := versionByPhase[payload.Phase]; exists && previous != version.PromptVersionID {
						t.Fatal("identical template created another version")
					}
					versionByPhase[payload.Phase] = version.PromptVersionID
				}
			}
			var persisted []researchaudit.PromptVersion
			if err := repository.DB().Where("version = ?", "research2-trailing5-v9").Order("phase").Find(&persisted).Error; err != nil {
				t.Fatal(err)
			}
			persistedPayloads, err := auditRepository.ListPayloads(ctx, researchaudit.OwnerResearch2, "legacy-run")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldVersions, persisted) || !reflect.DeepEqual(oldPayloads, persistedPayloads) {
				t.Fatal("historical audit changed")
			}
			var count int64
			if err := repository.DB().Model(&researchaudit.PromptVersion{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != int64(2+scenario.phases) {
				t.Fatalf("unexpected template count: %d", count)
			}
		})
	}
}
