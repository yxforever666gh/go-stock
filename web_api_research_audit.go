package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-stock/backend/researchaudit"

	"gorm.io/gorm"
)

type researchAuditRunStateDTO struct {
	Status       string    `json:"status"`
	PayloadCount int       `json:"payloadCount"`
	LastError    string    `json:"lastError"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type researchAuditPayloadDTO struct {
	PayloadID              string         `json:"payloadId"`
	Phase                  string         `json:"phase"`
	CallSequence           int            `json:"callSequence"`
	Attempt                int            `json:"attempt"`
	ProviderName           string         `json:"providerName"`
	ModelName              string         `json:"modelName"`
	ModelParameters        map[string]any `json:"modelParameters"`
	FinalPrompt            string         `json:"finalPrompt"`
	FinalPromptSHA256      string         `json:"finalPromptSha256"`
	EvidenceSnapshot       string         `json:"evidenceSnapshot"`
	EvidenceSHA256         string         `json:"evidenceSha256"`
	Tools                  []string       `json:"tools"`
	RawResponse            string         `json:"rawResponse"`
	RawResponseSHA256      string         `json:"rawResponseSha256"`
	RepairedResponse       string         `json:"repairedResponse"`
	RepairedResponseSHA256 string         `json:"repairedResponseSha256"`
	RepairLog              string         `json:"repairLog"`
	RedactionCount         int            `json:"redactionCount"`
	CreatedAt              time.Time      `json:"createdAt"`
}

type researchAuditDetailDTO struct {
	Availability string                    `json:"availability"`
	OwnerType    string                    `json:"ownerType"`
	OwnerID      string                    `json:"ownerId"`
	CutoffAt     *time.Time                `json:"cutoffAt"`
	State        researchAuditRunStateDTO  `json:"state"`
	Payloads     []researchAuditPayloadDTO `json:"payloads"`
}

type researchReplayDTO struct {
	ReplayID        string         `json:"replayId"`
	SourceOwnerType string         `json:"sourceOwnerType"`
	SourceOwnerID   string         `json:"sourceOwnerId"`
	ModelConfigID   int            `json:"modelConfigId"`
	Status          string         `json:"status"`
	CutoffAt        time.Time      `json:"cutoffAt"`
	CreatedAt       time.Time      `json:"createdAt"`
	StartedAt       *time.Time     `json:"startedAt"`
	CompletedAt     *time.Time     `json:"completedAt"`
	LastError       string         `json:"lastError"`
	Result          string         `json:"result"`
	ResultSHA256    string         `json:"resultSha256"`
	DiffSummary     map[string]any `json:"diffSummary"`
}

func auditDetailDTO(view researchaudit.AuditView) researchAuditDetailDTO {
	availability := "available"
	state := researchAuditRunStateDTO{Status: view.Status}
	if view.Status == researchaudit.StatusLegacyUnavailable {
		availability = researchaudit.StatusLegacyUnavailable
	}
	if view.State != nil {
		state = researchAuditRunStateDTO{Status: view.State.Status, PayloadCount: view.State.PayloadCount, LastError: stringValue(view.State.LastError), CreatedAt: view.State.CreatedAt, UpdatedAt: view.State.UpdatedAt}
	}
	result := researchAuditDetailDTO{Availability: availability, OwnerType: view.OwnerType, OwnerID: view.OwnerID, State: state, Payloads: make([]researchAuditPayloadDTO, 0, len(view.Payloads))}
	for _, payload := range view.Payloads {
		parameters := map[string]any{}
		_ = json.Unmarshal([]byte(payload.ModelParametersJSON), &parameters)
		tools := []string{}
		_ = json.Unmarshal([]byte(payload.ToolsJSON), &tools)
		manifest := researchaudit.RedactionManifest{}
		_ = json.Unmarshal([]byte(payload.RedactionManifestJSON), &manifest)
		result.Payloads = append(result.Payloads, researchAuditPayloadDTO{PayloadID: payload.PayloadID, Phase: payload.Phase, CallSequence: payload.CallSequence, Attempt: payload.Attempt, ProviderName: payload.ProviderName, ModelName: payload.ModelName, ModelParameters: parameters, FinalPrompt: payload.FinalPrompt, FinalPromptSHA256: payload.FinalPromptSHA256, EvidenceSnapshot: payload.Evidence, EvidenceSHA256: payload.EvidenceSHA256, Tools: tools, RawResponse: payload.RawResponse, RawResponseSHA256: stringValue(payload.RawResponseSHA256), RepairedResponse: payload.RepairedResponse, RepairedResponseSHA256: stringValue(payload.RepairedResponseSHA256), RepairLog: payload.RepairLog, RedactionCount: manifest.Count, CreatedAt: payload.CreatedAt})
		if payload.CutoffAt != nil && (result.CutoffAt == nil || payload.CutoffAt.Before(*result.CutoffAt)) {
			cutoff := *payload.CutoffAt
			result.CutoffAt = &cutoff
		}
	}
	return result
}

func replayDTO(view researchaudit.ReplayView) researchReplayDTO {
	replay := view.Replay
	result := researchReplayDTO{ReplayID: replay.ReplayID, SourceOwnerType: replay.SourceOwnerType, SourceOwnerID: replay.SourceOwnerID, ModelConfigID: replay.ModelConfigID, Status: replay.Status, CutoffAt: replay.CutoffAt, CreatedAt: replay.CreatedAt, StartedAt: replay.StartedAt, CompletedAt: replay.CompletedAt, LastError: stringValue(replay.LastError), DiffSummary: map[string]any{}}
	if view.Result != nil {
		result.Result, result.ResultSHA256 = view.Result.Result, view.Result.ResultSHA256
		_ = json.Unmarshal([]byte(view.Result.DiffSummaryJSON), &result.DiffSummary)
	}
	return result
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func writeResearchAuditError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, researchaudit.ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, researchaudit.ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
	case errors.Is(err, researchaudit.ErrImmutable):
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func registerResearchAuditRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/research/analysis-runs/{id}/audit", researchAuditDetailHandler(app, researchaudit.OwnerResearch1))
	mux.HandleFunc("GET /api/v1/research/analysis-runs/{id}/audit/export", researchAuditExportHandler(app, researchaudit.OwnerResearch1))
	mux.HandleFunc("GET /api/v1/research2/analysis-runs/{id}/audit", researchAuditDetailHandler(app, researchaudit.OwnerResearch2))
	mux.HandleFunc("GET /api/v1/research2/analysis-runs/{id}/audit/export", researchAuditExportHandler(app, researchaudit.OwnerResearch2))
	mux.HandleFunc("POST /api/v1/research/replays", func(w http.ResponseWriter, r *http.Request) {
		var request researchaudit.CreateReplayRequest
		if !decodeAPIRequest(w, r, &request) {
			return
		}
		view, err := app.createResearchReplay(r.Context(), request)
		if err != nil {
			writeResearchAuditError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, replayDTO(view))
	})
	mux.HandleFunc("GET /api/v1/research/replays/{id}", func(w http.ResponseWriter, r *http.Request) {
		view, err := app.getResearchReplay(r.Context(), strings.TrimSpace(r.PathValue("id")))
		if err != nil {
			writeResearchAuditError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, replayDTO(view))
	})
}

func researchAuditDetailHandler(app *App, ownerType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view, err := app.getResearchAudit(r.Context(), ownerType, strings.TrimSpace(r.PathValue("id")))
		if err != nil {
			writeResearchAuditError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, auditDetailDTO(view))
	}
}

func researchAuditExportHandler(app *App, ownerType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ownerID := strings.TrimSpace(r.PathValue("id"))
		body, err := app.exportResearchAudit(r.Context(), ownerType, ownerID)
		if err != nil {
			writeResearchAuditError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="research-audit-%s-%s.zip"`, ownerType, strings.ReplaceAll(ownerID, `"`, "")))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}
