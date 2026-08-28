package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-stock/backend/knowledge"
)

type knowledgeAPITestStub struct {
	knowledgeAPI
	decideVersion         func(context.Context, string, knowledge.VersionDecision) (knowledge.VersionState, error)
	createMemoryCandidate func(context.Context, knowledge.MemoryCandidateRequest) (knowledge.MemoryCandidate, error)
}

func (stub knowledgeAPITestStub) CreateMemoryCandidate(ctx context.Context, request knowledge.MemoryCandidateRequest) (knowledge.MemoryCandidate, error) {
	return stub.createMemoryCandidate(ctx, request)
}

func (stub knowledgeAPITestStub) DecideVersion(ctx context.Context, id string, decision knowledge.VersionDecision) (knowledge.VersionState, error) {
	return stub.decideVersion(ctx, id, decision)
}

func TestDecodeKnowledgeContentRejectsInvalidAndOversizedPayloads(t *testing.T) {
	t.Run("invalid base64", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		if _, ok := decodeKnowledgeContent(recorder, "***"); ok {
			t.Fatal("invalid base64 was accepted")
		}
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", recorder.Code)
		}
	})

	t.Run("decoded size limit", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		encoded := base64.StdEncoding.EncodeToString(make([]byte, knowledge.MaxDocumentBytes+1))
		if _, ok := decodeKnowledgeContent(recorder, encoded); ok {
			t.Fatal("oversized decoded document was accepted")
		}
		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d, want 413", recorder.Code)
		}
	})
}

func TestKnowledgeVersionDecisionIsAlwaysAttributedToLocalUser(t *testing.T) {
	var capturedID string
	var captured knowledge.VersionDecision
	service := knowledgeAPITestStub{decideVersion: func(_ context.Context, id string, decision knowledge.VersionDecision) (knowledge.VersionState, error) {
		capturedID, captured = id, decision
		return knowledge.VersionState{VersionID: id, Status: decision.Decision}, nil
	}}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/knowledge/versions/version-1/decision", strings.NewReader(`{"decision":"approved","reason":"reviewed"}`))
	req.SetPathValue("id", "version-1")
	recorder := httptest.NewRecorder()
	handleKnowledgeVersionDecision(service, recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if capturedID != "version-1" || captured.ActorType != knowledge.ActorUser || captured.ActorID != knowledgeLocalUserID || captured.Decision != knowledge.StateApproved {
		t.Fatalf("captured id=%q decision=%+v", capturedID, captured)
	}
}

func TestKnowledgeVersionDecisionRejectsNonTerminalDecision(t *testing.T) {
	called := false
	service := knowledgeAPITestStub{decideVersion: func(_ context.Context, _ string, decision knowledge.VersionDecision) (knowledge.VersionState, error) {
		called = true
		if decision.Decision != knowledge.StateDraft {
			t.Fatalf("decision=%q", decision.Decision)
		}
		return knowledge.VersionState{}, knowledge.ErrInvalidInput
	}}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/knowledge/versions/version-1/decision", strings.NewReader(`{"decision":"draft","reason":""}`))
	req.SetPathValue("id", "version-1")
	recorder := httptest.NewRecorder()
	handleKnowledgeVersionDecision(service, recorder, req)
	if !called || recorder.Code != http.StatusBadRequest {
		t.Fatalf("called=%v status=%d body=%s", called, recorder.Code, recorder.Body.String())
	}
}

func TestKnowledgeMemoryCandidateProvenanceDistinguishesAIReportFromCustomText(t *testing.T) {
	tests := []struct {
		name, content, wantActor, wantActorID string
	}{
		{name: "derived AI report", content: "", wantActor: knowledge.ActorAI, wantActorID: "research-memory-generator"},
		{name: "custom local text", content: "user-authored clue", wantActor: knowledge.ActorUser, wantActorID: knowledgeLocalUserID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var captured knowledge.MemoryCandidateRequest
			service := knowledgeAPITestStub{createMemoryCandidate: func(_ context.Context, request knowledge.MemoryCandidateRequest) (knowledge.MemoryCandidate, error) {
				captured = request
				return knowledge.MemoryCandidate{CandidateID: "candidate-1", SourceOwnerType: request.OwnerType, SourceOwnerID: request.OwnerID, Status: knowledge.StateDraft}, nil
			}}
			body := `{"sourceOwnerType":"research1","sourceOwnerId":"run-1","title":"candidate","content":"` + test.content + `"}`
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/knowledge/memory-candidates", strings.NewReader(body))
			recorder := httptest.NewRecorder()
			handleCreateKnowledgeMemoryCandidate(service, recorder, req)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if captured.ProposedByActorType != test.wantActor || captured.ProposedByActorID != test.wantActorID {
				t.Fatalf("provenance=%s/%s, want %s/%s", captured.ProposedByActorType, captured.ProposedByActorID, test.wantActor, test.wantActorID)
			}
		})
	}
}
