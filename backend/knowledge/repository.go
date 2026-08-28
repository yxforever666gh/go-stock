package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db, now: time.Now} }
func (r *Repository) DB() *gorm.DB          { return r.db }

type createDocumentRecord struct {
	Title, Description, DocumentType, OriginType, UserID string
	Tags                                                 []string
	SourceOwnerType, SourceOwnerID                       *string
	Filename, MimeType, Content                          string
}

func contentHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeTags(tags []string) ([]string, string) {
	seen := map[string]struct{}{}
	values := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, tag)
	}
	sort.Strings(values)
	encoded, _ := json.Marshal(values)
	return values, string(encoded)
}

func validateOwner(ownerType, ownerID string) bool {
	return (ownerType == "research1" || ownerType == "research2") && strings.TrimSpace(ownerID) != ""
}

func (r *Repository) createDocumentTx(tx *gorm.DB, record createDocumentRecord) (Document, DocumentVersion, error) {
	now := r.now().UTC()
	_, tagsJSON := normalizeTags(record.Tags)
	document := Document{DocumentID: uuid.NewString(), Title: strings.TrimSpace(record.Title), Description: strings.TrimSpace(record.Description), DocumentType: record.DocumentType, OriginType: record.OriginType, SourceOwnerType: record.SourceOwnerType, SourceOwnerID: record.SourceOwnerID, TagsJSON: tagsJSON, CreatedByUserID: strings.TrimSpace(record.UserID), CreatedAt: now, UpdatedAt: now}
	if document.Title == "" || document.CreatedByUserID == "" || record.Content == "" {
		return Document{}, DocumentVersion{}, ErrInvalidInput
	}
	if err := tx.Create(&document).Error; err != nil {
		return Document{}, DocumentVersion{}, err
	}
	version := DocumentVersion{VersionID: uuid.NewString(), DocumentID: document.DocumentID, VersionNo: 1, ContentText: record.Content, ContentSHA256: contentHash(record.Content), MimeType: record.MimeType, SourceFilename: record.Filename, ExtractionStatus: "complete", CreatedByUserID: document.CreatedByUserID, CreatedAt: now}
	if err := tx.Create(&version).Error; err != nil {
		return Document{}, DocumentVersion{}, err
	}
	state := VersionState{StateID: uuid.NewString(), VersionID: version.VersionID, Status: StateDraft, CreatedAt: now, UpdatedAt: now}
	if err := tx.Create(&state).Error; err != nil {
		return Document{}, DocumentVersion{}, err
	}
	version.Status, version.UpdatedAt = state.Status, state.UpdatedAt
	document.Tags, document.Versions = normalizeTagsOnly(record.Tags), []DocumentVersion{version}
	return document, version, nil
}

func normalizeTagsOnly(tags []string) []string {
	values, _ := normalizeTags(tags)
	return values
}

func (r *Repository) CreateDocument(ctx context.Context, record createDocumentRecord) (Document, error) {
	if r == nil || r.db == nil {
		return Document{}, errors.New("knowledge repository is unavailable")
	}
	var result Document
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		document, _, err := r.createDocumentTx(tx, record)
		result = document
		return err
	})
	return result, err
}

func (r *Repository) AddVersion(ctx context.Context, documentID, filename, mimeType, content, userID string) (DocumentVersion, error) {
	var result DocumentVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var document Document
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("document_id = ?", documentID).First(&document).Error; err != nil {
			return knowledgeNotFound(err)
		}
		var latest int
		if err := tx.Model(&DocumentVersion{}).Where("document_id = ?", documentID).Select("COALESCE(MAX(version_no), 0)").Scan(&latest).Error; err != nil {
			return err
		}
		now := r.now().UTC()
		result = DocumentVersion{VersionID: uuid.NewString(), DocumentID: documentID, VersionNo: latest + 1, ContentText: content, ContentSHA256: contentHash(content), MimeType: mimeType, SourceFilename: filename, ExtractionStatus: "complete", CreatedByUserID: userID, CreatedAt: now, Status: StateDraft, UpdatedAt: now}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		state := VersionState{StateID: uuid.NewString(), VersionID: result.VersionID, Status: StateDraft, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&state).Error; err != nil {
			return err
		}
		return tx.Model(&Document{}).Where("document_id = ?", documentID).Update("updated_at", now).Error
	})
	return result, err
}

func knowledgeNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func (r *Repository) GetDocument(ctx context.Context, documentID string) (Document, error) {
	var document Document
	if err := r.db.WithContext(ctx).Where("document_id = ?", documentID).First(&document).Error; err != nil {
		return Document{}, knowledgeNotFound(err)
	}
	_ = json.Unmarshal([]byte(document.TagsJSON), &document.Tags)
	if err := r.db.WithContext(ctx).Where("document_id = ?", documentID).Order("version_no DESC").Find(&document.Versions).Error; err != nil {
		return Document{}, err
	}
	for index := range document.Versions {
		var state VersionState
		if err := r.db.WithContext(ctx).Where("version_id = ?", document.Versions[index].VersionID).First(&state).Error; err != nil {
			return Document{}, err
		}
		document.Versions[index].Status, document.Versions[index].UpdatedAt = state.Status, state.UpdatedAt
		applyVersionDecision(&document.Versions[index], state)
	}
	return document, nil
}

func applyVersionDecision(version *DocumentVersion, state VersionState) {
	if version == nil {
		return
	}
	switch state.Status {
	case StateApproved:
		version.DecisionReason, version.DecidedBy, version.DecidedAt = pointerValue(state.ApprovalReason), pointerValue(state.ApprovedByUserID), state.ApprovedAt
	case StateRejected:
		version.DecisionReason, version.DecidedBy, version.DecidedAt = pointerValue(state.RejectionReason), pointerValue(state.RejectedByUserID), state.RejectedAt
	case StateSuperseded:
		version.DecisionReason, version.DecidedBy, version.DecidedAt = pointerValue(state.SupersededReason), pointerValue(state.SupersededByUserID), state.SupersededAt
	}
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (r *Repository) ListDocuments(ctx context.Context, limit, offset int) ([]Document, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var rows []Document
	if err := r.db.WithContext(ctx).Order("updated_at DESC, document_id ASC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, err
	}
	for index := range rows {
		_ = json.Unmarshal([]byte(rows[index].TagsJSON), &rows[index].Tags)
	}
	return rows, nil
}

func (r *Repository) ListDocumentsFiltered(ctx context.Context, status, query string, limit, offset int) ([]Document, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("knowledge repository is unavailable")
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	statement := r.db.WithContext(ctx).Table("knowledge_documents AS documents").
		Joins("JOIN knowledge_document_versions AS versions ON versions.document_id = documents.document_id").
		Joins("JOIN knowledge_version_states AS states ON states.version_id = versions.version_id").
		Where("versions.version_no = (SELECT MAX(latest.version_no) FROM knowledge_document_versions AS latest WHERE latest.document_id = documents.document_id)")
	if strings.TrimSpace(status) != "" {
		statement = statement.Where("states.status = ?", strings.TrimSpace(status))
	}
	if value := strings.TrimSpace(query); value != "" {
		pattern := "%" + strings.ToLower(value) + "%"
		statement = statement.Where("lower(documents.title) LIKE ? OR lower(versions.source_filename) LIKE ?", pattern, pattern)
	}
	var total int64
	if err := statement.Distinct("documents.document_id").Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Document
	if err := statement.Select("documents.*").Order("documents.updated_at DESC, documents.document_id ASC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	for index := range rows {
		_ = json.Unmarshal([]byte(rows[index].TagsJSON), &rows[index].Tags)
	}
	return rows, int(total), nil
}

type VersionDecision struct {
	Decision  string
	Reason    string
	ActorType string
	ActorID   string
}

func (r *Repository) DecideVersion(ctx context.Context, versionID string, decision VersionDecision) (VersionState, error) {
	if decision.ActorType != ActorUser || strings.TrimSpace(decision.ActorID) == "" {
		return VersionState{}, ErrApprovalForbidden
	}
	if decision.Decision != StateApproved && decision.Decision != StateRejected {
		return VersionState{}, ErrInvalidInput
	}
	var result VersionState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version DocumentVersion
		if err := tx.Where("version_id = ?", versionID).First(&version).Error; err != nil {
			return knowledgeNotFound(err)
		}
		var state VersionState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("version_id = ?", versionID).First(&state).Error; err != nil {
			return knowledgeNotFound(err)
		}
		if state.Status != StateDraft {
			return fmt.Errorf("%w: version is already %s", ErrConflict, state.Status)
		}
		now, actor, reason := r.now().UTC(), strings.TrimSpace(decision.ActorID), strings.TrimSpace(decision.Reason)
		if decision.Decision == StateApproved {
			var previous []VersionState
			if err := tx.Table("knowledge_version_states AS states").Select("states.*").Joins("JOIN knowledge_document_versions AS versions ON versions.version_id = states.version_id").Where("versions.document_id = ? AND states.status = ? AND states.version_id <> ?", version.DocumentID, StateApproved, versionID).Find(&previous).Error; err != nil {
				return err
			}
			for _, old := range previous {
				values := map[string]any{"status": StateSuperseded, "superseded_by_actor_type": ActorUser, "superseded_by_user_id": actor, "superseded_reason": "superseded by approved version " + versionID, "superseded_at": now, "updated_at": now}
				if err := tx.Model(&VersionState{}).Where("state_id = ? AND status = ?", old.StateID, StateApproved).Updates(values).Error; err != nil {
					return err
				}
			}
			values := map[string]any{"status": StateApproved, "approved_by_actor_type": ActorUser, "approved_by_user_id": actor, "approval_reason": reason, "approved_at": now, "updated_at": now}
			if result := tx.Model(&VersionState{}).Where("state_id = ? AND status = ?", state.StateID, StateDraft).Updates(values); result.Error != nil || result.RowsAffected != 1 {
				if result.Error != nil {
					return result.Error
				}
				return ErrConflict
			}
		} else {
			values := map[string]any{"status": StateRejected, "rejected_by_actor_type": ActorUser, "rejected_by_user_id": actor, "rejection_reason": reason, "rejected_at": now, "updated_at": now}
			if update := tx.Model(&VersionState{}).Where("state_id = ? AND status = ?", state.StateID, StateDraft).Updates(values); update.Error != nil || update.RowsAffected != 1 {
				if update.Error != nil {
					return update.Error
				}
				return ErrConflict
			}
		}
		if err := tx.Model(&Document{}).Where("document_id = ?", version.DocumentID).Update("updated_at", now).Error; err != nil {
			return err
		}
		return tx.Where("state_id = ?", state.StateID).First(&result).Error
	})
	return result, err
}

type ftsSearchRow struct {
	VersionID, DocumentID, Title, ContentText, ContentSHA256, MimeType, SourceFilename, Snippet string
	VersionNo                                                                                   int
	Score                                                                                       float64
}

func ftsExpression(query string) string {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 12 {
		fields = fields[:12]
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ReplaceAll(field, `"`, `""`)
		if field != "" {
			parts = append(parts, `"`+field+`"`)
		}
	}
	return strings.Join(parts, " OR ")
}

func (r *Repository) SearchApproved(ctx context.Context, query string, limit int) ([]ApprovedSearchHit, error) {
	return r.searchApproved(ctx, query, limit, nil)
}

func (r *Repository) SearchApprovedAt(ctx context.Context, query string, limit int, cutoff time.Time) ([]ApprovedSearchHit, error) {
	if cutoff.IsZero() {
		return nil, ErrInvalidInput
	}
	cutoff = cutoff.UTC()
	return r.searchApproved(ctx, query, limit, &cutoff)
}

func (r *Repository) searchApproved(ctx context.Context, query string, limit int, cutoff *time.Time) ([]ApprovedSearchHit, error) {
	if limit < 1 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	expression := ftsExpression(query)
	var rows []ftsSearchRow
	var err error
	stateSQL, stateArgs := "states.status = ?", []any{StateApproved}
	if cutoff != nil {
		stateSQL = "states.approved_at <= ? AND (states.status = ? OR (states.status = ? AND states.superseded_at > ?))"
		stateArgs = []any{*cutoff, StateApproved, StateSuperseded, *cutoff}
	}
	if expression != "" {
		statement := `SELECT versions.version_id, versions.document_id, documents.title,
  versions.version_no, versions.content_text, versions.content_sha256, versions.mime_type, versions.source_filename,
  snippet(knowledge_document_fts, 3, '[', ']', '…', 24) AS snippet,
  -bm25(knowledge_document_fts) AS score
FROM knowledge_document_fts
JOIN knowledge_document_versions AS versions ON versions.version_id = knowledge_document_fts.version_id
JOIN knowledge_documents AS documents ON documents.document_id = versions.document_id
JOIN knowledge_version_states AS states ON states.version_id = versions.version_id
WHERE knowledge_document_fts MATCH ? AND ` + stateSQL + `
ORDER BY bm25(knowledge_document_fts) ASC, versions.created_at DESC
LIMIT ?`
		args := append([]any{expression}, stateArgs...)
		args = append(args, limit)
		err = r.db.WithContext(ctx).Raw(statement, args...).Scan(&rows).Error
	}
	if expression == "" || err != nil || len(rows) == 0 {
		statement := r.db.WithContext(ctx).Table("knowledge_document_versions AS versions").Select("versions.version_id, versions.document_id, documents.title, versions.version_no, versions.content_text, versions.content_sha256, versions.mime_type, versions.source_filename, substr(versions.content_text, 1, 320) AS snippet, 0 AS score").Joins("JOIN knowledge_documents AS documents ON documents.document_id = versions.document_id").Joins("JOIN knowledge_version_states AS states ON states.version_id = versions.version_id").Where(stateSQL, stateArgs...)
		if strings.TrimSpace(query) != "" {
			terms := strings.Fields(query)
			if len(terms) > 12 {
				terms = terms[:12]
			}
			parts, args := make([]string, 0, len(terms)), make([]any, 0, len(terms)*2)
			for _, term := range terms {
				parts = append(parts, "(documents.title LIKE ? OR versions.content_text LIKE ?)")
				pattern := "%" + term + "%"
				args = append(args, pattern, pattern)
			}
			statement = statement.Where(strings.Join(parts, " OR "), args...)
		}
		err = statement.Order("versions.created_at DESC").Limit(limit).Scan(&rows).Error
	}
	if err != nil {
		return nil, err
	}
	result := make([]ApprovedSearchHit, 0, len(rows))
	for _, row := range rows {
		result = append(result, ApprovedSearchHit{VersionID: row.VersionID, DocumentID: row.DocumentID, Title: row.Title, VersionNo: row.VersionNo, ContentText: row.ContentText, ContentSHA256: row.ContentSHA256, MimeType: row.MimeType, SourceFilename: row.SourceFilename, Snippet: row.Snippet, Score: row.Score})
	}
	return result, nil
}

func (r *Repository) CreateMemoryCandidate(ctx context.Context, candidate *MemoryCandidate) error {
	if candidate == nil || !validateOwner(candidate.SourceOwnerType, candidate.SourceOwnerID) || strings.TrimSpace(candidate.Title) == "" || strings.TrimSpace(candidate.ContentText) == "" || !map[string]bool{ActorUser: true, ActorAI: true, ActorSystem: true}[candidate.ProposedByActorType] || strings.TrimSpace(candidate.ProposedByActorID) == "" {
		return ErrInvalidInput
	}
	if candidate.CandidateID == "" {
		candidate.CandidateID = uuid.NewString()
	}
	now := r.now().UTC()
	candidate.ContentText = strings.TrimSpace(candidate.ContentText)
	candidate.ContentSHA256, candidate.Status, candidate.CreatedAt, candidate.UpdatedAt = contentHash(candidate.ContentText), StateDraft, now, now
	return r.db.WithContext(ctx).Create(candidate).Error
}

func (r *Repository) GetMemoryCandidate(ctx context.Context, candidateID string) (MemoryCandidate, error) {
	var row MemoryCandidate
	if err := r.db.WithContext(ctx).Where("candidate_id = ?", candidateID).First(&row).Error; err != nil {
		return MemoryCandidate{}, knowledgeNotFound(err)
	}
	return row, nil
}

func (r *Repository) ListMemoryCandidates(ctx context.Context, limit, offset int) ([]MemoryCandidate, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var rows []MemoryCandidate
	err := r.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, err
}

type CandidateDecision struct {
	Decision, Reason, ActorType, ActorID string
}

func (r *Repository) DecideMemoryCandidate(ctx context.Context, candidateID string, decision CandidateDecision) (MemoryCandidate, error) {
	if decision.ActorType != ActorUser || strings.TrimSpace(decision.ActorID) == "" {
		return MemoryCandidate{}, ErrApprovalForbidden
	}
	if decision.Decision != StateApproved && decision.Decision != StateRejected {
		return MemoryCandidate{}, ErrInvalidInput
	}
	var result MemoryCandidate
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidate MemoryCandidate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("candidate_id = ?", candidateID).First(&candidate).Error; err != nil {
			return knowledgeNotFound(err)
		}
		if candidate.Status != StateDraft {
			return fmt.Errorf("%w: candidate is already %s", ErrConflict, candidate.Status)
		}
		now, actor, reason := r.now().UTC(), strings.TrimSpace(decision.ActorID), strings.TrimSpace(decision.Reason)
		values := map[string]any{"status": decision.Decision, "decision_actor_type": ActorUser, "decision_by_user_id": actor, "decision_reason": reason, "updated_at": now}
		if decision.Decision == StateApproved {
			ownerType, ownerID := candidate.SourceOwnerType, candidate.SourceOwnerID
			document, version, err := r.createDocumentTx(tx, createDocumentRecord{Title: candidate.Title, DocumentType: "memory", OriginType: "memory_candidate", UserID: actor, SourceOwnerType: &ownerType, SourceOwnerID: &ownerID, Filename: "memory-" + candidate.CandidateID + ".md", MimeType: "text/markdown", Content: candidate.ContentText})
			if err != nil {
				return err
			}
			state, err := r.approveDraftStateTx(tx, version.VersionID, actor, "approved memory candidate: "+reason)
			if err != nil {
				return err
			}
			_ = state
			values["approved_version_id"] = version.VersionID
			if err := tx.Model(&Document{}).Where("document_id = ?", document.DocumentID).Update("updated_at", now).Error; err != nil {
				return err
			}
		}
		update := tx.Model(&MemoryCandidate{}).Where("candidate_id = ? AND status = ?", candidateID, StateDraft).Updates(values)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrConflict
		}
		return tx.Where("candidate_id = ?", candidateID).First(&result).Error
	})
	return result, err
}

func (r *Repository) approveDraftStateTx(tx *gorm.DB, versionID, actor, reason string) (VersionState, error) {
	var state VersionState
	if err := tx.Where("version_id = ?", versionID).First(&state).Error; err != nil {
		return state, err
	}
	now := r.now().UTC()
	values := map[string]any{"status": StateApproved, "approved_by_actor_type": ActorUser, "approved_by_user_id": actor, "approval_reason": reason, "approved_at": now, "updated_at": now}
	update := tx.Model(&VersionState{}).Where("state_id = ? AND status = ?", state.StateID, StateDraft).Updates(values)
	if update.Error != nil || update.RowsAffected != 1 {
		if update.Error != nil {
			return state, update.Error
		}
		return state, ErrConflict
	}
	return state, tx.Where("state_id = ?", state.StateID).First(&state).Error
}

func (r *Repository) RecordRetrieval(ctx context.Context, run *RetrievalRun, hits []RetrievalHit) error {
	if run == nil || !validateOwner(run.OwnerType, run.OwnerID) || run.CutoffAt.IsZero() {
		return ErrInvalidInput
	}
	if run.RetrievalRunID == "" {
		run.RetrievalRunID = uuid.NewString()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = r.now().UTC()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(run).Error; err != nil {
			return err
		}
		for index := range hits {
			if hits[index].RetrievalHitID == "" {
				hits[index].RetrievalHitID = uuid.NewString()
			}
			hits[index].RetrievalRunID, hits[index].Rank = run.RetrievalRunID, index+1
			if hits[index].CreatedAt.IsZero() {
				hits[index].CreatedAt = run.CreatedAt
			}
			if hits[index].VerificationStatus == "" {
				hits[index].VerificationStatus = "unverified"
			}
			if hits[index].EvidenceRefsJSON == "" {
				hits[index].EvidenceRefsJSON = "[]"
			}
			if err := tx.Create(&hits[index]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
