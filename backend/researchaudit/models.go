package researchaudit

import "time"

const (
	OwnerResearch1 = "research1"
	OwnerResearch2 = "research2"
	OwnerReplay    = "replay"

	StatusCapturing         = "capturing"
	StatusComplete          = "complete"
	StatusFailed            = "failed"
	StatusLegacyUnavailable = "legacy_unavailable"
)

type PromptVersion struct {
	PromptVersionID string    `gorm:"column:prompt_version_id;primaryKey" json:"promptVersionId"`
	ResearchScope   string    `gorm:"column:research_scope" json:"researchScope"`
	Phase           string    `gorm:"column:phase" json:"phase"`
	Version         string    `gorm:"column:version" json:"version"`
	TemplateCodec   string    `gorm:"column:template_codec" json:"templateCodec"`
	TemplateBlob    []byte    `gorm:"column:template_blob" json:"-"`
	TemplateSHA256  string    `gorm:"column:template_sha256" json:"templateSha256"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"createdAt"`
}

func (PromptVersion) TableName() string { return "research_audit_prompt_versions" }

type Payload struct {
	PayloadID              string     `gorm:"column:payload_id;primaryKey" json:"payloadId"`
	OwnerType              string     `gorm:"column:owner_type" json:"ownerType"`
	OwnerID                string     `gorm:"column:owner_id" json:"ownerId"`
	PromptVersionID        *string    `gorm:"column:prompt_version_id" json:"promptVersionId,omitempty"`
	Phase                  string     `gorm:"column:phase" json:"phase"`
	CallSequence           int        `gorm:"column:call_sequence" json:"callSequence"`
	Attempt                int        `gorm:"column:attempt" json:"attempt"`
	ProviderName           string     `gorm:"column:provider_name" json:"providerName"`
	ModelName              string     `gorm:"column:model_name" json:"modelName"`
	ModelParametersJSON    string     `gorm:"column:model_parameters_json" json:"modelParameters"`
	CutoffAt               *time.Time `gorm:"column:cutoff_at" json:"cutoffAt,omitempty"`
	FinalPromptCodec       string     `gorm:"column:final_prompt_codec" json:"finalPromptCodec"`
	FinalPromptBlob        []byte     `gorm:"column:final_prompt_blob" json:"-"`
	FinalPromptSHA256      string     `gorm:"column:final_prompt_sha256" json:"finalPromptSha256"`
	EvidenceCodec          string     `gorm:"column:evidence_codec" json:"evidenceCodec"`
	EvidenceBlob           []byte     `gorm:"column:evidence_blob" json:"-"`
	EvidenceSHA256         string     `gorm:"column:evidence_sha256" json:"evidenceSha256"`
	ToolsJSON              string     `gorm:"column:tools_json" json:"tools"`
	RawResponseCodec       *string    `gorm:"column:raw_response_codec" json:"rawResponseCodec,omitempty"`
	RawResponseBlob        []byte     `gorm:"column:raw_response_blob" json:"-"`
	RawResponseSHA256      *string    `gorm:"column:raw_response_sha256" json:"rawResponseSha256,omitempty"`
	RepairedResponseCodec  *string    `gorm:"column:repaired_response_codec" json:"repairedResponseCodec,omitempty"`
	RepairedResponseBlob   []byte     `gorm:"column:repaired_response_blob" json:"-"`
	RepairedResponseSHA256 *string    `gorm:"column:repaired_response_sha256" json:"repairedResponseSha256,omitempty"`
	RepairLogCodec         *string    `gorm:"column:repair_log_codec" json:"repairLogCodec,omitempty"`
	RepairLogBlob          []byte     `gorm:"column:repair_log_blob" json:"-"`
	RepairLogSHA256        *string    `gorm:"column:repair_log_sha256" json:"repairLogSha256,omitempty"`
	RedactionManifestJSON  string     `gorm:"column:redaction_manifest_json" json:"redactionManifest"`
	CreatedAt              time.Time  `gorm:"column:created_at" json:"createdAt"`
}

func (Payload) TableName() string { return "research_audit_payloads" }

type RunState struct {
	OwnerType    string    `gorm:"column:owner_type;primaryKey" json:"ownerType"`
	OwnerID      string    `gorm:"column:owner_id;primaryKey" json:"ownerId"`
	Status       string    `gorm:"column:status" json:"status"`
	PayloadCount int       `gorm:"column:payload_count" json:"payloadCount"`
	LastError    *string   `gorm:"column:last_error" json:"lastError,omitempty"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

func (RunState) TableName() string { return "research_audit_run_states" }

type Replay struct {
	ReplayID        string     `gorm:"column:replay_id;primaryKey" json:"replayId"`
	SourceOwnerType string     `gorm:"column:source_owner_type" json:"sourceOwnerType"`
	SourceOwnerID   string     `gorm:"column:source_owner_id" json:"sourceOwnerId"`
	ModelConfigID   int        `gorm:"column:model_config_id" json:"modelConfigId"`
	Status          string     `gorm:"column:status" json:"status"`
	CutoffAt        time.Time  `gorm:"column:cutoff_at" json:"cutoffAt"`
	CreatedAt       time.Time  `gorm:"column:created_at" json:"createdAt"`
	StartedAt       *time.Time `gorm:"column:started_at" json:"startedAt,omitempty"`
	CompletedAt     *time.Time `gorm:"column:completed_at" json:"completedAt,omitempty"`
	LastError       *string    `gorm:"column:last_error" json:"lastError,omitempty"`
}

func (Replay) TableName() string { return "research_replays" }

type ReplayResult struct {
	ReplayResultID  string    `gorm:"column:replay_result_id;primaryKey" json:"replayResultId"`
	ReplayID        string    `gorm:"column:replay_id" json:"replayId"`
	ResultCodec     string    `gorm:"column:result_codec" json:"resultCodec"`
	ResultBlob      []byte    `gorm:"column:result_blob" json:"-"`
	ResultSHA256    string    `gorm:"column:result_sha256" json:"resultSha256"`
	DiffSummaryJSON string    `gorm:"column:diff_summary_json" json:"diffSummary"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"createdAt"`
}

func (ReplayResult) TableName() string { return "research_replay_results" }

type DecodedPayload struct {
	Payload
	FinalPrompt      string `json:"finalPrompt"`
	Evidence         string `json:"evidence"`
	RawResponse      string `json:"rawResponse,omitempty"`
	RepairedResponse string `json:"repairedResponse,omitempty"`
	RepairLog        string `json:"repairLog,omitempty"`
}

type AuditView struct {
	OwnerType string           `json:"ownerType"`
	OwnerID   string           `json:"ownerId"`
	Status    string           `json:"status"`
	State     *RunState        `json:"state,omitempty"`
	Payloads  []DecodedPayload `json:"payloads"`
}

type ReplayView struct {
	Replay Replay         `json:"replay"`
	Result *DecodedResult `json:"result,omitempty"`
}

type DecodedResult struct {
	ReplayResult
	Result string `json:"result"`
}
