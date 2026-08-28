package main

import (
	"encoding/json"
	"testing"
	"time"

	"go-stock/backend/researchaudit"
)

func TestAuditDetailDTOUsesLockedPublicFieldShapes(t *testing.T) {
	now := time.Date(2026, 8, 28, 1, 55, 0, 0, time.UTC)
	rawHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	view := researchaudit.AuditView{OwnerType: researchaudit.OwnerResearch1, OwnerID: "run-1", Status: researchaudit.StatusComplete, State: &researchaudit.RunState{Status: researchaudit.StatusComplete, PayloadCount: 1, CreatedAt: now, UpdatedAt: now}, Payloads: []researchaudit.DecodedPayload{{Payload: researchaudit.Payload{PayloadID: "payload-1", Phase: "market", CallSequence: 1, Attempt: 1, ProviderName: "provider", ModelName: "model", ModelParametersJSON: `{"temperature":0}`, CutoffAt: &now, FinalPromptSHA256: rawHash, EvidenceSHA256: rawHash, ToolsJSON: `["search"]`, RawResponseSHA256: &rawHash, RedactionManifestJSON: `{"fields":["header.authorization"],"count":1}`, CreatedAt: now}, FinalPrompt: "prompt", Evidence: "evidence", RawResponse: "raw"}}}
	dto := auditDetailDTO(view)
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if body["availability"] != "available" || body["ownerId"] != "run-1" || body["cutoffAt"] == nil {
		t.Fatalf("detail=%s", encoded)
	}
	payload := body["payloads"].([]any)[0].(map[string]any)
	if payload["evidenceSnapshot"] != "evidence" || payload["modelParameters"].(map[string]any)["temperature"].(float64) != 0 || payload["tools"].([]any)[0] != "search" || payload["redactionCount"].(float64) != 1 {
		t.Fatalf("payload=%v", payload)
	}
	for _, required := range []string{"rawResponse", "rawResponseSha256", "repairedResponse", "repairedResponseSha256", "repairLog"} {
		if _, ok := payload[required]; !ok {
			t.Fatalf("required field %s missing from %v", required, payload)
		}
	}
}

func TestReplayDTOIsFlatAndUsesNullTimes(t *testing.T) {
	now := time.Date(2026, 8, 28, 1, 55, 0, 0, time.UTC)
	dto := replayDTO(researchaudit.ReplayView{Replay: researchaudit.Replay{ReplayID: "replay-1", SourceOwnerType: researchaudit.OwnerResearch2, SourceOwnerID: "run-2", ModelConfigID: 7, Status: "queued", CutoffAt: now, CreatedAt: now}})
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if _, nested := body["replay"]; nested || body["replayId"] != "replay-1" || body["startedAt"] != nil || body["completedAt"] != nil || body["result"] != "" {
		t.Fatalf("replay=%s", encoded)
	}
}
