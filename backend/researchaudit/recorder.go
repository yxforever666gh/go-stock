package researchaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type CallInput struct {
	OwnerType       string
	OwnerID         string
	Phase           string
	CallSequence    int
	Attempt         int
	ProviderName    string
	ModelName       string
	ModelParameters any
	CutoffAt        *time.Time
	Prompt          string
	Messages        any
	Evidence        any
	Tools           []string
	Template        string
	TemplateVersion string
}

type PreparedCall struct {
	input               CallInput
	Prompt              string
	Messages            any
	modelParametersJSON string
	evidenceJSON        string
	toolsJSON           string
	promptVersionID     *string
	redactions          []RedactionManifest
}

type CallResult struct {
	RawResponse      string
	RepairedResponse string
	RepairLog        string
	ProviderName     string
	ModelName        string
	ActualConfigID   uint
	ModelParameters  any
}

type Recorder struct{ repository *Repository }

func NewRecorder(repository *Repository) *Recorder { return &Recorder{repository: repository} }

func (r *Recorder) Begin(ctx context.Context, ownerType, ownerID string) error {
	_, err := r.repository.BeginRun(ctx, ownerType, ownerID)
	return err
}

func (r *Recorder) Complete(ctx context.Context, ownerType, ownerID string) error {
	return r.repository.FinishRun(ctx, ownerType, ownerID, StatusComplete, "")
}

func (r *Recorder) Fail(ctx context.Context, ownerType, ownerID string, cause error) error {
	message := ""
	if cause != nil {
		message, _ = RedactText(cause.Error())
	}
	return r.repository.FinishRun(ctx, ownerType, ownerID, StatusFailed, message)
}

func marshalAndRedact(value any, fallback string) (string, RedactionManifest, error) {
	if value == nil {
		return fallback, RedactionManifest{}, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", RedactionManifest{}, err
	}
	redacted, manifest := RedactText(string(encoded))
	return redacted, manifest, nil
}

func (r *Recorder) Prepare(ctx context.Context, input CallInput) (PreparedCall, error) {
	if r == nil || r.repository == nil || !validOwner(input.OwnerType, true) || strings.TrimSpace(input.OwnerID) == "" || strings.TrimSpace(input.Phase) == "" || input.CallSequence < 1 {
		return PreparedCall{}, ErrInvalidRequest
	}
	if input.Attempt < 1 {
		input.Attempt = 1
	}
	prompt, promptManifest := RedactText(input.Prompt)
	params, paramsManifest, err := marshalAndRedact(input.ModelParameters, "{}")
	if err != nil {
		return PreparedCall{}, err
	}
	evidence, evidenceManifest, err := marshalAndRedact(input.Evidence, "{}")
	if err != nil {
		return PreparedCall{}, err
	}
	tools, toolsManifest, err := marshalAndRedact(input.Tools, "[]")
	if err != nil {
		return PreparedCall{}, err
	}
	prepared := PreparedCall{input: input, Prompt: prompt, modelParametersJSON: params, evidenceJSON: evidence, toolsJSON: tools, redactions: []RedactionManifest{promptManifest, paramsManifest, evidenceManifest, toolsManifest}}
	if input.Messages != nil {
		messages, messageManifest, marshalErr := marshalAndRedact(input.Messages, "[]")
		if marshalErr != nil {
			return PreparedCall{}, marshalErr
		}
		prepared.Messages = json.RawMessage(messages)
		prepared.redactions = append(prepared.redactions, messageManifest)
	}
	if input.OwnerType != OwnerReplay {
		template := strings.TrimSpace(input.Template)
		if template == "" {
			// When a caller has no separately compiled template, the exact redacted
			// rendered prompt is itself the immutable versioned artifact. This avoids
			// a synthetic phase label that could not reproduce what was sent.
			template = prompt
		}
		template, templateManifest := RedactText(template)
		prepared.redactions = append(prepared.redactions, templateManifest)
		version := strings.TrimSpace(input.TemplateVersion)
		if version == "" {
			_, digest, digestErr := encodeGZIP(template)
			if digestErr != nil {
				return PreparedCall{}, digestErr
			}
			version = "2.3.0-" + digest[:12]
		}
		row, ensureErr := r.repository.EnsurePromptVersion(ctx, input.OwnerType, input.Phase, version, template)
		if ensureErr != nil {
			return PreparedCall{}, ensureErr
		}
		prepared.promptVersionID = &row.PromptVersionID
	}
	return prepared, nil
}

func optionalBundle(value string) (*string, []byte, *string, error) {
	if value == "" {
		return nil, nil, nil, nil
	}
	blob, digest, err := encodeGZIP(value)
	if err != nil {
		return nil, nil, nil, err
	}
	codec := "gzip"
	return &codec, blob, &digest, nil
}

func (r *Recorder) Record(ctx context.Context, prepared PreparedCall, result CallResult) error {
	if prepared.input.OwnerID == "" {
		return ErrInvalidRequest
	}
	raw, rawManifest := RedactText(result.RawResponse)
	repaired, repairedManifest := RedactText(result.RepairedResponse)
	repairLog, logManifest := RedactText(result.RepairLog)
	prepared.redactions = append(prepared.redactions, rawManifest, repairedManifest, logManifest)
	if result.ActualConfigID > 0 || result.ModelParameters != nil {
		parameters := map[string]any{}
		_ = json.Unmarshal([]byte(prepared.modelParametersJSON), &parameters)
		if result.ActualConfigID > 0 {
			parameters["actualConfigId"] = result.ActualConfigID
		}
		if result.ModelParameters != nil {
			parameters["actualModel"] = result.ModelParameters
		}
		updated, manifest, marshalErr := marshalAndRedact(parameters, "{}")
		if marshalErr != nil {
			return marshalErr
		}
		prepared.modelParametersJSON = updated
		prepared.redactions = append(prepared.redactions, manifest)
	}
	promptBlob, promptHash, err := encodeGZIP(prepared.Prompt)
	if err != nil {
		return err
	}
	evidenceBlob, evidenceHash, err := encodeGZIP(prepared.evidenceJSON)
	if err != nil {
		return err
	}
	rawCodec, rawBlob, rawHash, err := optionalBundle(raw)
	if err != nil {
		return err
	}
	repairedCodec, repairedBlob, repairedHash, err := optionalBundle(repaired)
	if err != nil {
		return err
	}
	logCodec, logBlob, logHash, err := optionalBundle(repairLog)
	if err != nil {
		return err
	}
	manifest := RedactionManifest{}
	seen := map[string]struct{}{}
	for _, item := range prepared.redactions {
		for _, field := range item.Fields {
			seen[field] = struct{}{}
		}
	}
	for field := range seen {
		manifest.Fields = append(manifest.Fields, field)
	}
	manifest.Count = len(manifest.Fields)
	manifestJSON, _ := json.Marshal(manifest)
	provider := strings.TrimSpace(result.ProviderName)
	if provider == "" {
		provider = prepared.input.ProviderName
	}
	if provider == "" {
		provider = "unavailable"
	}
	model := strings.TrimSpace(result.ModelName)
	if model == "" {
		model = prepared.input.ModelName
	}
	if model == "" {
		model = "unavailable"
	}
	payload := &Payload{OwnerType: prepared.input.OwnerType, OwnerID: prepared.input.OwnerID, PromptVersionID: prepared.promptVersionID, Phase: prepared.input.Phase, CallSequence: prepared.input.CallSequence, Attempt: prepared.input.Attempt, ProviderName: provider, ModelName: model, ModelParametersJSON: prepared.modelParametersJSON, CutoffAt: prepared.input.CutoffAt, FinalPromptCodec: "gzip", FinalPromptBlob: promptBlob, FinalPromptSHA256: promptHash, EvidenceCodec: "gzip", EvidenceBlob: evidenceBlob, EvidenceSHA256: evidenceHash, ToolsJSON: prepared.toolsJSON, RawResponseCodec: rawCodec, RawResponseBlob: rawBlob, RawResponseSHA256: rawHash, RepairedResponseCodec: repairedCodec, RepairedResponseBlob: repairedBlob, RepairedResponseSHA256: repairedHash, RepairLogCodec: logCodec, RepairLogBlob: logBlob, RepairLogSHA256: logHash, RedactionManifestJSON: string(manifestJSON)}
	return r.repository.InsertPayload(ctx, payload)
}

func (r *Recorder) Audit(ctx context.Context, ownerType, ownerID string) (AuditView, error) {
	state, err := r.repository.GetRunState(ctx, ownerType, ownerID)
	if errors.Is(err, ErrNotFound) {
		return AuditView{OwnerType: ownerType, OwnerID: ownerID, Status: StatusLegacyUnavailable, Payloads: []DecodedPayload{}}, nil
	}
	if err != nil {
		return AuditView{}, err
	}
	rows, err := r.repository.ListPayloads(ctx, ownerType, ownerID)
	if err != nil {
		return AuditView{}, err
	}
	view := AuditView{OwnerType: ownerType, OwnerID: ownerID, Status: state.Status, State: &state, Payloads: make([]DecodedPayload, 0, len(rows))}
	for _, row := range rows {
		decoded, decodeErr := decodePayload(row)
		if decodeErr != nil {
			return AuditView{}, fmt.Errorf("decode payload %s: %w", row.PayloadID, decodeErr)
		}
		view.Payloads = append(view.Payloads, decoded)
	}
	return view, nil
}

func decodePayload(row Payload) (DecodedPayload, error) {
	result := DecodedPayload{Payload: row}
	var err error
	if result.FinalPrompt, err = decodeGZIP(row.FinalPromptBlob, row.FinalPromptSHA256); err != nil {
		return result, err
	}
	if result.Evidence, err = decodeGZIP(row.EvidenceBlob, row.EvidenceSHA256); err != nil {
		return result, err
	}
	if row.RawResponseCodec != nil {
		result.RawResponse, err = decodeGZIP(row.RawResponseBlob, deref(row.RawResponseSHA256))
	}
	if err == nil && row.RepairedResponseCodec != nil {
		result.RepairedResponse, err = decodeGZIP(row.RepairedResponseBlob, deref(row.RepairedResponseSHA256))
	}
	if err == nil && row.RepairLogCodec != nil {
		result.RepairLog, err = decodeGZIP(row.RepairLogBlob, deref(row.RepairLogSHA256))
	}
	return result, err
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
