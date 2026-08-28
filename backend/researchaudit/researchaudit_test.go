package researchaudit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type secretReplayExecutor struct{}

func (secretReplayExecutor) CompleteReplay(context.Context, ReplayCall) (ReplayCallResult, error) {
	return ReplayCallResult{}, fmt.Errorf("provider failed: Authorization: Bearer replay-secret https://example.test/v1?api_key=query-secret")
}

func auditTestRepository(t *testing.T) *Repository {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:audit-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AutoMigrate(&PromptVersion{}, &Payload{}, &RunState{}, &Replay{}, &ReplayResult{}); err != nil {
		t.Fatal(err)
	}
	repository := NewRepository(database)
	repository.now = func() time.Time { return time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC) }
	return repository
}

func TestRedactTextCoversHeadersURLAndNestedJSON(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer bearer-secret",
		"Proxy-Authorization: Basic basic-secret",
		"Cookie: sid=cookie-secret; token=cookie-token",
		"https://example.test/path?symbol=600000&token=url-token&api_key=url-key",
		`{"outer":{"password":"json-password","safe":"visible"}}`,
	}, "\n")
	redacted, manifest := RedactText(input)
	for _, secret := range []string{"bearer-secret", "basic-secret", "cookie-secret", "cookie-token", "url-token", "url-key", "json-password"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q leaked in %q", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "symbol=600000") || !strings.Contains(redacted, "visible") {
		t.Fatalf("non-sensitive content was removed: %q", redacted)
	}
	if manifest.Count < 5 {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestRecorderSavesExactlyPreparedPromptAndImmutableHash(t *testing.T) {
	repository := auditTestRepository(t)
	recorder := NewRecorder(repository)
	ctx := context.Background()
	if err := recorder.Begin(ctx, OwnerResearch1, "run-1"); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 8, 28, 1, 55, 0, 0, time.UTC)
	prepared, err := recorder.Prepare(ctx, CallInput{OwnerType: OwnerResearch1, OwnerID: "run-1", Phase: "market", CallSequence: 1, Prompt: "Authorization: Bearer top-secret\n分析", Evidence: map[string]any{"token": "evidence-secret", "value": 7}, ModelParameters: map[string]any{"password": "model-secret"}, Tools: []string{"search"}, CutoffAt: &cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prepared.Prompt, "top-secret") {
		t.Fatalf("prepared prompt leaked: %q", prepared.Prompt)
	}
	// This is the exact string a runner passes to AIClient after Prepare.
	modelReceived := prepared.Prompt
	if err = recorder.Record(ctx, prepared, CallResult{RawResponse: "ok", ProviderName: "fixture", ModelName: "model"}); err != nil {
		t.Fatal(err)
	}
	if err = recorder.Complete(ctx, OwnerResearch1, "run-1"); err != nil {
		t.Fatal(err)
	}
	view, err := recorder.Audit(ctx, OwnerResearch1, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Payloads) != 1 || view.Payloads[0].FinalPrompt != modelReceived {
		t.Fatalf("saved prompt differs from sent prompt: %+v", view.Payloads)
	}
	if view.Payloads[0].PromptVersionID == nil {
		t.Fatal("rendered prompt did not receive an immutable prompt version")
	}
	var version PromptVersion
	if err = repository.db.First(&version, "prompt_version_id = ?", *view.Payloads[0].PromptVersionID).Error; err != nil {
		t.Fatal(err)
	}
	template, decodeErr := decodeGZIP(version.TemplateBlob, version.TemplateSHA256)
	if decodeErr != nil || template != modelReceived || !strings.HasPrefix(version.Version, "2.3.0-") {
		t.Fatalf("prompt version is not the exact sent template: version=%+v template=%q err=%v", version, template, decodeErr)
	}
	_, digest, _ := encodeGZIP(modelReceived)
	if view.Payloads[0].FinalPromptSHA256 != digest {
		t.Fatalf("hash=%s want=%s", view.Payloads[0].FinalPromptSHA256, digest)
	}
	if err = repository.InsertPayload(ctx, &Payload{OwnerType: OwnerResearch1, OwnerID: "run-1", CallSequence: 2, Attempt: 1}); err == nil {
		t.Fatal("completed audit accepted another payload")
	}
}

func TestFailureStateAndExportRedactErrorSecrets(t *testing.T) {
	repository := auditTestRepository(t)
	recorder := NewRecorder(repository)
	ctx := context.Background()
	if err := recorder.Begin(ctx, OwnerResearch1, "failed-run"); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Fail(ctx, OwnerResearch1, "failed-run", fmt.Errorf("Authorization: Bearer state-secret https://example.test?token=url-secret")); err != nil {
		t.Fatal(err)
	}
	view, err := recorder.Audit(ctx, OwnerResearch1, "failed-run")
	if err != nil || view.State == nil || view.State.LastError == nil {
		t.Fatalf("failed state=%+v err=%v", view.State, err)
	}
	if strings.Contains(*view.State.LastError, "state-secret") || strings.Contains(*view.State.LastError, "url-secret") {
		t.Fatalf("failed state leaked secret: %q", *view.State.LastError)
	}
}

func TestReplayFailureRedactsLastError(t *testing.T) {
	repository := auditTestRepository(t)
	recorder := NewRecorder(repository)
	ctx := context.Background()
	if err := recorder.Begin(ctx, OwnerResearch1, "replay-source"); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 8, 28, 1, 55, 0, 0, time.UTC)
	prepared, err := recorder.Prepare(ctx, CallInput{OwnerType: OwnerResearch1, OwnerID: "replay-source", Phase: "final", CallSequence: 1, Prompt: "fixed", Evidence: map[string]any{"fixed": true}, CutoffAt: &cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if err = recorder.Record(ctx, prepared, CallResult{RawResponse: "original", ProviderName: "fixture", ModelName: "model"}); err != nil {
		t.Fatal(err)
	}
	if err = recorder.Complete(ctx, OwnerResearch1, "replay-source"); err != nil {
		t.Fatal(err)
	}
	service := NewService(repository)
	replay, err := service.CreateReplay(ctx, CreateReplayRequest{SourceOwnerType: OwnerResearch1, SourceOwnerID: "replay-source", ModelConfigID: 1})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := service.ExecuteReplay(ctx, replay.ReplayID, secretReplayExecutor{})
	if err == nil || failed.Replay.LastError == nil {
		t.Fatalf("replay should fail with persisted error: view=%+v err=%v", failed, err)
	}
	if strings.Contains(*failed.Replay.LastError, "replay-secret") || strings.Contains(*failed.Replay.LastError, "query-secret") {
		t.Fatalf("replay last_error leaked secret: %q", *failed.Replay.LastError)
	}
}

type fixtureReplayExecutor struct{ calls []ReplayCall }

func (f *fixtureReplayExecutor) CompleteReplay(_ context.Context, call ReplayCall) (ReplayCallResult, error) {
	f.calls = append(f.calls, call)
	return ReplayCallResult{Content: `{"answer":"different"}`, ProviderName: "selected-provider", ModelName: "selected-model", AttemptLog: []string{"ok"}}, nil
}

func TestReplayUsesFrozenPayloadAndOnlyAuditTables(t *testing.T) {
	repository := auditTestRepository(t)
	database := repository.db
	if err := database.Exec(`CREATE TABLE research_v160_recommendations (id INTEGER PRIMARY KEY, marker TEXT); INSERT INTO research_v160_recommendations(marker) VALUES ('unchanged')`).Error; err != nil {
		t.Fatal(err)
	}
	recorder := NewRecorder(repository)
	ctx := context.Background()
	if err := recorder.Begin(ctx, OwnerResearch1, "source-run"); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 8, 28, 1, 55, 0, 0, time.UTC)
	prepared, err := recorder.Prepare(ctx, CallInput{OwnerType: OwnerResearch1, OwnerID: "source-run", Phase: "final", CallSequence: 1, Prompt: "fixed prompt", Evidence: map[string]any{"fixed": true}, CutoffAt: &cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if err = recorder.Record(ctx, prepared, CallResult{RawResponse: `{"answer":"original"}`, ProviderName: "old", ModelName: "old-model"}); err != nil {
		t.Fatal(err)
	}
	if err = recorder.Complete(ctx, OwnerResearch1, "source-run"); err != nil {
		t.Fatal(err)
	}
	service := NewService(repository)
	replay, err := service.CreateReplay(ctx, CreateReplayRequest{SourceOwnerType: OwnerResearch1, SourceOwnerID: "source-run", ModelConfigID: 9})
	if err != nil {
		t.Fatal(err)
	}
	executor := &fixtureReplayExecutor{}
	view, err := service.ExecuteReplay(ctx, replay.ReplayID, executor)
	if err != nil {
		t.Fatal(err)
	}
	if view.Replay.Status != "completed" || view.Result == nil || len(executor.calls) != 1 {
		t.Fatalf("view=%+v calls=%+v", view, executor.calls)
	}
	if executor.calls[0].Prompt != "fixed prompt" || !executor.calls[0].CutoffEvidenceMatches(`{"fixed":true}`) {
		t.Fatalf("replay did not use frozen input: %+v", executor.calls[0])
	}
	var count int64
	if err = database.Table("research_v160_recommendations").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("formal recommendation table changed: count=%d err=%v", count, err)
	}
	var diff map[string]any
	if err = json.Unmarshal([]byte(view.Result.DiffSummaryJSON), &diff); err != nil || diff["changedCount"].(float64) != 1 {
		t.Fatalf("diff=%v err=%v", diff, err)
	}
}

func (call ReplayCall) CutoffEvidenceMatches(expected string) bool {
	var left, right any
	return json.Unmarshal([]byte(call.Evidence), &left) == nil && json.Unmarshal([]byte(expected), &right) == nil && fmt.Sprint(left) == fmt.Sprint(right)
}

func TestLegacyAuditDoesNotReconstructPayloads(t *testing.T) {
	recorder := NewRecorder(auditTestRepository(t))
	view, err := recorder.Audit(context.Background(), OwnerResearch2, "old-run")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != StatusLegacyUnavailable || len(view.Payloads) != 0 || view.State != nil {
		t.Fatalf("legacy view=%+v", view)
	}
}

func TestExportProducesRedactedAuditZIP(t *testing.T) {
	repository := auditTestRepository(t)
	recorder := NewRecorder(repository)
	ctx := context.Background()
	if err := recorder.Begin(ctx, OwnerResearch1, "export-run"); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 8, 28, 1, 55, 0, 0, time.UTC)
	prepared, err := recorder.Prepare(ctx, CallInput{OwnerType: OwnerResearch1, OwnerID: "export-run", Phase: "market", CallSequence: 1, Prompt: "api_key=export-secret", Evidence: map[string]any{"value": true}, CutoffAt: &cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if err = recorder.Record(ctx, prepared, CallResult{RawResponse: "ok", ProviderName: "fixture", ModelName: "model"}); err != nil {
		t.Fatal(err)
	}
	if err = recorder.Complete(ctx, OwnerResearch1, "export-run"); err != nil {
		t.Fatal(err)
	}
	bundle, err := NewService(repository).Export(ctx, OwnerResearch1, "export-run")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil || len(reader.File) != 1 || reader.File[0].Name != "audit.json" {
		t.Fatalf("zip entries=%v err=%v", reader.File, err)
	}
	file, err := reader.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(file)
	_ = file.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(body), "export-secret") || !strings.Contains(string(body), redactedValue) {
		t.Fatalf("unsafe export: %s", body)
	}
}
