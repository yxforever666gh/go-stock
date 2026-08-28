package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"go-stock/backend/db"
	"go-stock/backend/knowledge"
)

const (
	knowledgeLocalUserID     = "local-user"
	maxKnowledgeRequestBytes = int64(15 << 20)
)

type knowledgeAPI interface {
	CreateDocument(context.Context, knowledge.CreateDocumentRequest) (knowledge.Document, error)
	AddVersion(context.Context, string, knowledge.AddVersionRequest) (knowledge.DocumentVersion, error)
	GetDocument(context.Context, string) (knowledge.Document, error)
	ListDocuments(context.Context, int, int) ([]knowledge.Document, error)
	ListDocumentsFiltered(context.Context, string, string, int, int) ([]knowledge.Document, int, error)
	DecideVersion(context.Context, string, knowledge.VersionDecision) (knowledge.VersionState, error)
	SearchApproved(context.Context, string, int) ([]knowledge.ApprovedSearchHit, error)
	CreateFromResearch(context.Context, knowledge.ResearchDraftRequest) (knowledge.Document, error)
	CreateMemoryCandidate(context.Context, knowledge.MemoryCandidateRequest) (knowledge.MemoryCandidate, error)
	ListMemoryCandidates(context.Context, int, int) ([]knowledge.MemoryCandidate, error)
	DecideMemoryCandidate(context.Context, string, knowledge.CandidateDecision) (knowledge.MemoryCandidate, error)
}

var knowledgeServiceFactory = func(app *App) knowledgeAPI {
	return knowledge.NewService(knowledge.NewRepository(db.Dao), appKnowledgeReportLoader{app: app})
}

type appKnowledgeReportLoader struct{ app *App }

func (loader appKnowledgeReportLoader) LoadResearchReport(ctx context.Context, ownerType, ownerID string) (knowledge.ResearchReport, error) {
	if loader.app == nil {
		return knowledge.ResearchReport{}, errors.New("knowledge report loader is unavailable")
	}
	switch ownerType {
	case "research1":
		run, err := loader.app.getAIAnalysisReport(ctx, ownerID)
		if err != nil {
			return knowledge.ResearchReport{}, fmt.Errorf("%w: research1 %s: %v", knowledge.ErrNotFound, ownerID, err)
		}
		parts := []string{
			markdownSection("市场分析", run.MarketReport),
			markdownSection("板块分析", run.SectorReport),
			markdownSection("个股分析", run.StockReport),
			markdownSection("最终报告", run.FinalReport),
		}
		return knowledge.ResearchReport{Title: "Research 1 " + ownerID, Content: strings.TrimSpace(strings.Join(parts, "\n\n"))}, nil
	case "research2":
		run, err := loader.app.getResearch2Run(ctx, ownerID)
		if err != nil {
			return knowledge.ResearchReport{}, fmt.Errorf("%w: research2 %s: %v", knowledge.ErrNotFound, ownerID, err)
		}
		return knowledge.ResearchReport{Title: "Research 2 " + ownerID, Content: strings.TrimSpace(run.ReportMarkdown)}, nil
	default:
		return knowledge.ResearchReport{}, knowledge.ErrInvalidInput
	}
}

func markdownSection(title, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return "## " + title + "\n\n" + content
}

type createKnowledgeDocumentRequest struct {
	Title         string `json:"title"`
	Filename      string `json:"filename"`
	MimeType      string `json:"mimeType"`
	ContentBase64 string `json:"contentBase64"`
}

type createKnowledgeFromResearchRequest struct {
	SourceOwnerType string `json:"sourceOwnerType"`
	SourceOwnerID   string `json:"sourceOwnerId"`
	Title           string `json:"title"`
}

type createKnowledgeMemoryCandidateRequest struct {
	SourceOwnerType string `json:"sourceOwnerType"`
	SourceOwnerID   string `json:"sourceOwnerId"`
	Title           string `json:"title"`
	Content         string `json:"content"`
}

type knowledgeDecisionRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type knowledgeDocumentSummaryDTO struct {
	DocumentID           string    `json:"documentId"`
	Title                string    `json:"title"`
	DocumentType         string    `json:"documentType"`
	OriginType           string    `json:"originType"`
	SourceOwnerType      string    `json:"sourceOwnerType"`
	SourceOwnerID        string    `json:"sourceOwnerId"`
	LatestVersionNumber  int       `json:"latestVersionNumber"`
	LatestStatus         string    `json:"latestStatus"`
	LatestSourceFilename string    `json:"latestSourceFilename"`
	LatestContentSHA256  string    `json:"latestContentSha256"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type knowledgeDocumentVersionDTO struct {
	VersionID        string     `json:"versionId"`
	DocumentID       string     `json:"documentId"`
	VersionNumber    int        `json:"versionNumber"`
	Status           string     `json:"status"`
	MimeType         string     `json:"mimeType"`
	SourceFilename   string     `json:"sourceFilename"`
	ContentText      string     `json:"contentText"`
	ContentSHA256    string     `json:"contentSha256"`
	ExtractionStatus string     `json:"extractionStatus"`
	CreatedBy        string     `json:"createdBy"`
	CreatedAt        time.Time  `json:"createdAt"`
	DecisionReason   string     `json:"decisionReason"`
	DecidedBy        string     `json:"decidedBy"`
	DecidedAt        *time.Time `json:"decidedAt"`
}

type knowledgeDocumentDetailDTO struct {
	Document      knowledgeDocumentSummaryDTO `json:"document"`
	LatestVersion knowledgeDocumentVersionDTO `json:"latestVersion"`
}

type knowledgeDocumentPageDTO struct {
	Items    []knowledgeDocumentSummaryDTO `json:"items"`
	Total    int                           `json:"total"`
	Page     int                           `json:"page"`
	PageSize int                           `json:"pageSize"`
}

type knowledgeSearchHitDTO struct {
	DocumentID      string  `json:"documentId"`
	VersionID       string  `json:"versionId"`
	Title           string  `json:"title"`
	Excerpt         string  `json:"excerpt"`
	Score           float64 `json:"score"`
	ContentSHA256   string  `json:"contentSha256"`
	SourceOwnerType string  `json:"sourceOwnerType"`
	SourceOwnerID   string  `json:"sourceOwnerId"`
	Status          string  `json:"status"`
}

type knowledgeMemoryCandidateDTO struct {
	CandidateID       string     `json:"candidateId"`
	SourceOwnerType   string     `json:"sourceOwnerType"`
	SourceOwnerID     string     `json:"sourceOwnerId"`
	Title             string     `json:"title"`
	Content           string     `json:"content"`
	ContentSHA256     string     `json:"contentSha256"`
	Status            string     `json:"status"`
	ApprovedVersionID string     `json:"approvedVersionId"`
	DecisionReason    string     `json:"decisionReason"`
	DecidedBy         string     `json:"decidedBy"`
	CreatedAt         time.Time  `json:"createdAt"`
	DecidedAt         *time.Time `json:"decidedAt"`
}

func registerKnowledgeRoutes(mux *http.ServeMux, app *App) {
	service := knowledgeServiceFactory(app)
	mux.HandleFunc("GET /api/v1/knowledge/documents", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeDocuments(service, w, r) })
	mux.HandleFunc("POST /api/v1/knowledge/documents", func(w http.ResponseWriter, r *http.Request) { handleCreateKnowledgeDocument(service, w, r) })
	mux.HandleFunc("POST /api/v1/knowledge/documents/from-research", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeFromResearch(service, w, r) })
	mux.HandleFunc("GET /api/v1/knowledge/documents/{id}", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeDocument(service, w, r) })
	mux.HandleFunc("GET /api/v1/knowledge/documents/{id}/versions", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeVersions(service, w, r) })
	mux.HandleFunc("POST /api/v1/knowledge/documents/{id}/versions", func(w http.ResponseWriter, r *http.Request) { handleCreateKnowledgeVersion(service, w, r) })
	mux.HandleFunc("POST /api/v1/knowledge/versions/{id}/decision", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeVersionDecision(service, w, r) })
	mux.HandleFunc("GET /api/v1/knowledge/search", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeSearch(service, w, r) })
	mux.HandleFunc("GET /api/v1/knowledge/memory-candidates", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeMemoryCandidates(service, w, r) })
	mux.HandleFunc("POST /api/v1/knowledge/memory-candidates", func(w http.ResponseWriter, r *http.Request) { handleCreateKnowledgeMemoryCandidate(service, w, r) })
	mux.HandleFunc("POST /api/v1/knowledge/memory-candidates/{id}/decision", func(w http.ResponseWriter, r *http.Request) { handleKnowledgeMemoryCandidateDecision(service, w, r) })
}

func handleKnowledgeDocuments(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	page, err := queryBoundedInt(r, "page", 1, 1, 1_000_000)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	pageSize, err := queryBoundedInt(r, "pageSize", 20, 1, 100)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	status, query := strings.TrimSpace(r.URL.Query().Get("status")), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if status != "" && !knowledgeVersionStatus(status) {
		writeKnowledgeError(w, knowledge.ErrInvalidInput)
		return
	}
	offset := (page - 1) * pageSize
	rows, total, err := service.ListDocumentsFiltered(r.Context(), status, query, pageSize, offset)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	items := make([]knowledgeDocumentSummaryDTO, 0, len(rows))
	for _, row := range rows {
		detail, detailErr := service.GetDocument(r.Context(), row.DocumentID)
		if detailErr != nil {
			writeKnowledgeError(w, detailErr)
			return
		}
		summary, ok := knowledgeDocumentSummary(detail)
		if !ok {
			continue
		}
		items = append(items, summary)
	}
	writeJSON(w, http.StatusOK, knowledgeDocumentPageDTO{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func handleCreateKnowledgeDocument(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	var request createKnowledgeDocumentRequest
	if !decodeKnowledgeRequest(w, r, &request) {
		return
	}
	content, ok := decodeKnowledgeContent(w, request.ContentBase64)
	if !ok {
		return
	}
	document, err := service.CreateDocument(r.Context(), knowledge.CreateDocumentRequest{Title: request.Title, Filename: request.Filename, MimeType: request.MimeType, UserID: knowledgeLocalUserID, Data: content})
	writeKnowledgeDocumentResult(service, w, document, err, http.StatusCreated)
}

func handleKnowledgeFromResearch(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	var request createKnowledgeFromResearchRequest
	if !decodeKnowledgeRequest(w, r, &request) {
		return
	}
	document, err := service.CreateFromResearch(r.Context(), knowledge.ResearchDraftRequest{OwnerType: request.SourceOwnerType, OwnerID: request.SourceOwnerID, Title: request.Title, UserID: knowledgeLocalUserID})
	writeKnowledgeDocumentResult(service, w, document, err, http.StatusCreated)
}

func handleKnowledgeDocument(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	document, err := service.GetDocument(r.Context(), strings.TrimSpace(r.PathValue("id")))
	writeKnowledgeDocumentResult(service, w, document, err, http.StatusOK)
}

func handleKnowledgeVersions(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	document, err := service.GetDocument(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	result := make([]knowledgeDocumentVersionDTO, 0, len(document.Versions))
	for _, version := range document.Versions {
		result = append(result, knowledgeVersionDTO(version))
	}
	writeJSON(w, http.StatusOK, result)
}

func handleCreateKnowledgeVersion(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	var request createKnowledgeDocumentRequest
	if !decodeKnowledgeRequest(w, r, &request) {
		return
	}
	content, ok := decodeKnowledgeContent(w, request.ContentBase64)
	if !ok {
		return
	}
	documentID := strings.TrimSpace(r.PathValue("id"))
	_, err := service.AddVersion(r.Context(), documentID, knowledge.AddVersionRequest{Filename: request.Filename, MimeType: request.MimeType, UserID: knowledgeLocalUserID, Data: content})
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	document, err := service.GetDocument(r.Context(), documentID)
	writeKnowledgeDocumentResult(service, w, document, err, http.StatusCreated)
}

func handleKnowledgeVersionDecision(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	var request knowledgeDecisionRequest
	if !decodeKnowledgeRequest(w, r, &request) {
		return
	}
	state, err := service.DecideVersion(r.Context(), strings.TrimSpace(r.PathValue("id")), knowledge.VersionDecision{Decision: request.Decision, Reason: request.Reason, ActorType: knowledge.ActorUser, ActorID: knowledgeLocalUserID})
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func handleKnowledgeSearch(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeKnowledgeError(w, knowledge.ErrInvalidInput)
		return
	}
	limit, err := queryBoundedInt(r, "limit", 20, 1, 50)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	hits, err := service.SearchApproved(r.Context(), query, limit)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	result := make([]knowledgeSearchHitDTO, 0, len(hits))
	for _, hit := range hits {
		document, documentErr := service.GetDocument(r.Context(), hit.DocumentID)
		if documentErr != nil {
			writeKnowledgeError(w, documentErr)
			return
		}
		ownerType, ownerID := pointerString(document.SourceOwnerType), pointerString(document.SourceOwnerID)
		result = append(result, knowledgeSearchHitDTO{DocumentID: hit.DocumentID, VersionID: hit.VersionID, Title: hit.Title, Excerpt: hit.Snippet, Score: hit.Score, ContentSHA256: hit.ContentSHA256, SourceOwnerType: ownerType, SourceOwnerID: ownerID, Status: knowledge.StateApproved})
	}
	writeJSON(w, http.StatusOK, result)
}

func handleKnowledgeMemoryCandidates(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != knowledge.StateDraft && status != knowledge.StateApproved && status != knowledge.StateRejected {
		writeKnowledgeError(w, knowledge.ErrInvalidInput)
		return
	}
	rows, err := service.ListMemoryCandidates(r.Context(), 100, 0)
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	result := make([]knowledgeMemoryCandidateDTO, 0, len(rows))
	for _, row := range rows {
		if status == "" || row.Status == status {
			result = append(result, knowledgeMemoryDTO(row))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func handleCreateKnowledgeMemoryCandidate(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	var request createKnowledgeMemoryCandidateRequest
	if !decodeKnowledgeRequest(w, r, &request) {
		return
	}
	actorType, actorID := knowledge.ActorAI, "research-memory-generator"
	if strings.TrimSpace(request.Content) != "" {
		// Custom content supplied through the local HTTP API is authored by the
		// local user. An empty content field asks the service to derive the
		// candidate from the selected AI research report, so that provenance is AI.
		actorType, actorID = knowledge.ActorUser, knowledgeLocalUserID
	}
	candidate, err := service.CreateMemoryCandidate(r.Context(), knowledge.MemoryCandidateRequest{OwnerType: request.SourceOwnerType, OwnerID: request.SourceOwnerID, Title: request.Title, Content: request.Content, ProposedByActorType: actorType, ProposedByActorID: actorID})
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, knowledgeMemoryDTO(candidate))
}

func handleKnowledgeMemoryCandidateDecision(service knowledgeAPI, w http.ResponseWriter, r *http.Request) {
	var request knowledgeDecisionRequest
	if !decodeKnowledgeRequest(w, r, &request) {
		return
	}
	candidate, err := service.DecideMemoryCandidate(r.Context(), strings.TrimSpace(r.PathValue("id")), knowledge.CandidateDecision{Decision: request.Decision, Reason: request.Reason, ActorType: knowledge.ActorUser, ActorID: knowledgeLocalUserID})
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, knowledgeMemoryDTO(candidate))
}

func decodeKnowledgeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := decodeJSONRequest(w, r, target, maxKnowledgeRequestBytes, false); err != nil {
		writeRequestError(w, err)
		return false
	}
	return true
}

func decodeKnowledgeContent(w http.ResponseWriter, encoded string) ([]byte, bool) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		writeKnowledgeError(w, knowledge.ErrInvalidInput)
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		writeKnowledgeError(w, fmt.Errorf("%w: invalid contentBase64", knowledge.ErrInvalidInput))
		return nil, false
	}
	if len(decoded) > knowledge.MaxDocumentBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "decoded document exceeds 10 MiB"})
		return nil, false
	}
	return decoded, true
}

func writeKnowledgeDocumentResult(service knowledgeAPI, w http.ResponseWriter, document knowledge.Document, err error, status int) {
	if err != nil {
		writeKnowledgeError(w, err)
		return
	}
	if len(document.Versions) == 0 {
		var getErr error
		document, getErr = service.GetDocument(context.Background(), document.DocumentID)
		if getErr != nil {
			writeKnowledgeError(w, getErr)
			return
		}
	}
	summary, ok := knowledgeDocumentSummary(document)
	if !ok {
		writeKnowledgeError(w, knowledge.ErrNotFound)
		return
	}
	writeJSON(w, status, knowledgeDocumentDetailDTO{Document: summary, LatestVersion: knowledgeVersionDTO(document.Versions[0])})
}

func knowledgeDocumentSummary(document knowledge.Document) (knowledgeDocumentSummaryDTO, bool) {
	if len(document.Versions) == 0 {
		return knowledgeDocumentSummaryDTO{}, false
	}
	versions := append([]knowledge.DocumentVersion(nil), document.Versions...)
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].VersionNo > versions[j].VersionNo })
	latest := versions[0]
	return knowledgeDocumentSummaryDTO{DocumentID: document.DocumentID, Title: document.Title, DocumentType: document.DocumentType, OriginType: document.OriginType, SourceOwnerType: pointerString(document.SourceOwnerType), SourceOwnerID: pointerString(document.SourceOwnerID), LatestVersionNumber: latest.VersionNo, LatestStatus: latest.Status, LatestSourceFilename: latest.SourceFilename, LatestContentSHA256: latest.ContentSHA256, CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt}, true
}

func knowledgeVersionDTO(version knowledge.DocumentVersion) knowledgeDocumentVersionDTO {
	return knowledgeDocumentVersionDTO{VersionID: version.VersionID, DocumentID: version.DocumentID, VersionNumber: version.VersionNo, Status: version.Status, MimeType: version.MimeType, SourceFilename: version.SourceFilename, ContentText: version.ContentText, ContentSHA256: version.ContentSHA256, ExtractionStatus: version.ExtractionStatus, CreatedBy: knowledge.ActorUser, CreatedAt: version.CreatedAt, DecisionReason: version.DecisionReason, DecidedBy: version.DecidedBy, DecidedAt: version.DecidedAt}
}

func knowledgeMemoryDTO(candidate knowledge.MemoryCandidate) knowledgeMemoryCandidateDTO {
	decidedBy, reason := pointerString(candidate.DecisionByUserID), pointerString(candidate.DecisionReason)
	var decidedAt *time.Time
	if candidate.Status != knowledge.StateDraft {
		value := candidate.UpdatedAt
		decidedAt = &value
	}
	return knowledgeMemoryCandidateDTO{CandidateID: candidate.CandidateID, SourceOwnerType: candidate.SourceOwnerType, SourceOwnerID: candidate.SourceOwnerID, Title: candidate.Title, Content: candidate.ContentText, ContentSHA256: candidate.ContentSHA256, Status: candidate.Status, ApprovedVersionID: pointerString(candidate.ApprovedVersionID), DecisionReason: reason, DecidedBy: decidedBy, CreatedAt: candidate.CreatedAt, DecidedAt: decidedAt}
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func knowledgeVersionStatus(value string) bool {
	return value == knowledge.StateDraft || value == knowledge.StateApproved || value == knowledge.StateRejected || value == knowledge.StateSuperseded
}

func writeKnowledgeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, knowledge.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, knowledge.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, knowledge.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, knowledge.ErrApprovalForbidden):
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
