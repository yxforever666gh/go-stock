package knowledge

import "time"

const (
	StateDraft      = "draft"
	StateApproved   = "approved"
	StateRejected   = "rejected"
	StateSuperseded = "superseded"

	ActorUser   = "user"
	ActorAI     = "ai"
	ActorSystem = "system"
)

type Document struct {
	DocumentID      string            `gorm:"column:document_id;primaryKey" json:"documentId"`
	Title           string            `gorm:"column:title" json:"title"`
	DocumentType    string            `gorm:"column:document_type" json:"documentType"`
	OriginType      string            `gorm:"column:origin_type" json:"originType"`
	SourceOwnerType *string           `gorm:"column:source_owner_type" json:"sourceOwnerType,omitempty"`
	SourceOwnerID   *string           `gorm:"column:source_owner_id" json:"sourceOwnerId,omitempty"`
	Description     string            `gorm:"column:description" json:"description"`
	TagsJSON        string            `gorm:"column:tags_json" json:"-"`
	CreatedByUserID string            `gorm:"column:created_by_user_id" json:"createdByUserId"`
	CreatedAt       time.Time         `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt       time.Time         `gorm:"column:updated_at" json:"updatedAt"`
	Tags            []string          `gorm:"-" json:"tags"`
	Versions        []DocumentVersion `gorm:"-" json:"versions,omitempty"`
}

func (Document) TableName() string { return "knowledge_documents" }

type DocumentVersion struct {
	VersionID        string     `gorm:"column:version_id;primaryKey" json:"versionId"`
	DocumentID       string     `gorm:"column:document_id" json:"documentId"`
	VersionNo        int        `gorm:"column:version_no" json:"versionNo"`
	ContentText      string     `gorm:"column:content_text" json:"contentText"`
	ContentSHA256    string     `gorm:"column:content_sha256" json:"contentSha256"`
	MimeType         string     `gorm:"column:mime_type" json:"mimeType"`
	SourceFilename   string     `gorm:"column:source_filename" json:"sourceFilename"`
	ExtractionStatus string     `gorm:"column:extraction_status" json:"extractionStatus"`
	ExtractionError  string     `gorm:"column:extraction_error" json:"extractionError"`
	CreatedByUserID  string     `gorm:"column:created_by_user_id" json:"createdByUserId"`
	CreatedAt        time.Time  `gorm:"column:created_at" json:"createdAt"`
	Status           string     `gorm:"-" json:"status"`
	UpdatedAt        time.Time  `gorm:"-" json:"updatedAt"`
	DecisionReason   string     `gorm:"-" json:"decisionReason"`
	DecidedBy        string     `gorm:"-" json:"decidedBy"`
	DecidedAt        *time.Time `gorm:"-" json:"decidedAt,omitempty"`
}

func (DocumentVersion) TableName() string { return "knowledge_document_versions" }

type VersionState struct {
	StateID               string     `gorm:"column:state_id;primaryKey" json:"stateId"`
	VersionID             string     `gorm:"column:version_id" json:"versionId"`
	Status                string     `gorm:"column:status" json:"status"`
	ApprovedByActorType   *string    `gorm:"column:approved_by_actor_type" json:"approvedByActorType,omitempty"`
	ApprovedByUserID      *string    `gorm:"column:approved_by_user_id" json:"approvedByUserId,omitempty"`
	ApprovalReason        *string    `gorm:"column:approval_reason" json:"approvalReason,omitempty"`
	ApprovedAt            *time.Time `gorm:"column:approved_at" json:"approvedAt,omitempty"`
	RejectedByActorType   *string    `gorm:"column:rejected_by_actor_type" json:"rejectedByActorType,omitempty"`
	RejectedByUserID      *string    `gorm:"column:rejected_by_user_id" json:"rejectedByUserId,omitempty"`
	RejectionReason       *string    `gorm:"column:rejection_reason" json:"rejectionReason,omitempty"`
	RejectedAt            *time.Time `gorm:"column:rejected_at" json:"rejectedAt,omitempty"`
	SupersededByActorType *string    `gorm:"column:superseded_by_actor_type" json:"supersededByActorType,omitempty"`
	SupersededByUserID    *string    `gorm:"column:superseded_by_user_id" json:"supersededByUserId,omitempty"`
	SupersededReason      *string    `gorm:"column:superseded_reason" json:"supersededReason,omitempty"`
	SupersededAt          *time.Time `gorm:"column:superseded_at" json:"supersededAt,omitempty"`
	CreatedAt             time.Time  `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt             time.Time  `gorm:"column:updated_at" json:"updatedAt"`
}

func (VersionState) TableName() string { return "knowledge_version_states" }

type MemoryCandidate struct {
	CandidateID         string    `gorm:"column:candidate_id;primaryKey" json:"candidateId"`
	SourceOwnerType     string    `gorm:"column:source_owner_type" json:"sourceOwnerType"`
	SourceOwnerID       string    `gorm:"column:source_owner_id" json:"sourceOwnerId"`
	Title               string    `gorm:"column:title" json:"title"`
	ContentText         string    `gorm:"column:content_text" json:"contentText"`
	ContentSHA256       string    `gorm:"column:content_sha256" json:"contentSha256"`
	Status              string    `gorm:"column:status" json:"status"`
	ProposedByActorType string    `gorm:"column:proposed_by_actor_type" json:"proposedByActorType"`
	ProposedByActorID   string    `gorm:"column:proposed_by_actor_id" json:"proposedByActorId"`
	DecisionActorType   *string   `gorm:"column:decision_actor_type" json:"decisionActorType,omitempty"`
	DecisionByUserID    *string   `gorm:"column:decision_by_user_id" json:"decisionByUserId,omitempty"`
	DecisionReason      *string   `gorm:"column:decision_reason" json:"decisionReason,omitempty"`
	ApprovedVersionID   *string   `gorm:"column:approved_version_id" json:"approvedVersionId,omitempty"`
	CreatedAt           time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt           time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

func (MemoryCandidate) TableName() string { return "knowledge_memory_candidates" }

type RetrievalRun struct {
	RetrievalRunID      string    `gorm:"column:retrieval_run_id;primaryKey" json:"retrievalRunId"`
	OwnerType           string    `gorm:"column:owner_type" json:"ownerType"`
	OwnerID             string    `gorm:"column:owner_id" json:"ownerId"`
	CutoffAt            time.Time `gorm:"column:cutoff_at" json:"cutoffAt"`
	QueryText           string    `gorm:"column:query_text" json:"queryText"`
	ExperimentalEnabled bool      `gorm:"column:experimental_enabled" json:"experimentalEnabled"`
	CreatedAt           time.Time `gorm:"column:created_at" json:"createdAt"`
}

func (RetrievalRun) TableName() string { return "knowledge_retrieval_runs" }

type RetrievalHit struct {
	RetrievalHitID     string    `gorm:"column:retrieval_hit_id;primaryKey" json:"retrievalHitId"`
	RetrievalRunID     string    `gorm:"column:retrieval_run_id" json:"retrievalRunId"`
	VersionID          string    `gorm:"column:version_id" json:"versionId"`
	Rank               int       `gorm:"column:rank" json:"rank"`
	Score              float64   `gorm:"column:score" json:"score"`
	Adopted            bool      `gorm:"column:adopted" json:"adopted"`
	AdoptionReason     string    `gorm:"column:adoption_reason" json:"adoptionReason"`
	VerificationStatus string    `gorm:"column:verification_status" json:"verificationStatus"`
	VerificationReason string    `gorm:"column:verification_reason" json:"verificationReason"`
	EvidenceSetID      *string   `gorm:"column:evidence_set_id" json:"evidenceSetId,omitempty"`
	EvidenceItemID     *string   `gorm:"column:evidence_item_id" json:"evidenceItemId,omitempty"`
	EvidenceRefsJSON   string    `gorm:"column:evidence_refs_json" json:"-"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"createdAt"`
	EvidenceRefs       []string  `gorm:"-" json:"evidenceRefs"`
	DocumentID         string    `gorm:"-" json:"documentId,omitempty"`
	Title              string    `gorm:"-" json:"title,omitempty"`
	VersionNo          int       `gorm:"-" json:"versionNo,omitempty"`
	Snippet            string    `gorm:"-" json:"snippet,omitempty"`
}

func (RetrievalHit) TableName() string { return "knowledge_retrieval_hits" }

type ApprovedSearchHit struct {
	VersionID      string  `json:"versionId"`
	DocumentID     string  `json:"documentId"`
	Title          string  `json:"title"`
	VersionNo      int     `json:"versionNo"`
	ContentText    string  `json:"contentText"`
	ContentSHA256  string  `json:"contentSha256"`
	MimeType       string  `json:"mimeType"`
	SourceFilename string  `json:"sourceFilename"`
	Snippet        string  `json:"snippet"`
	Score          float64 `json:"score"`
}
