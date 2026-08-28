package researchaudit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ReplayCall struct {
	Phase         string
	Prompt        string
	Evidence      string
	ModelConfigID int
}

type ReplayCallResult struct {
	Content         string
	ProviderName    string
	ModelName       string
	AttemptLog      any
	ModelParameters any
}

type ReplayExecutor interface {
	CompleteReplay(context.Context, ReplayCall) (ReplayCallResult, error)
}

type Service struct {
	repository *Repository
	recorder   *Recorder
	now        func() time.Time
}

func NewService(repository *Repository) *Service {
	return &Service{repository: repository, recorder: NewRecorder(repository), now: time.Now}
}

func (s *Service) Audit(ctx context.Context, ownerType, ownerID string) (AuditView, error) {
	if !validOwner(ownerType, false) || strings.TrimSpace(ownerID) == "" {
		return AuditView{}, ErrInvalidRequest
	}
	return s.recorder.Audit(ctx, ownerType, ownerID)
}

func (s *Service) Export(ctx context.Context, ownerType, ownerID string) ([]byte, error) {
	view, err := s.Audit(ctx, ownerType, ownerID)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	header := &zip.FileHeader{Name: "audit.json", Method: zip.Deflate}
	header.SetModTime(time.Unix(0, 0).UTC())
	file, err := writer.CreateHeader(header)
	if err == nil {
		_, err = file.Write(encoded)
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type CreateReplayRequest struct {
	SourceOwnerType string `json:"sourceOwnerType"`
	SourceOwnerID   string `json:"sourceOwnerId"`
	ModelConfigID   int    `json:"modelConfigId"`
}

func (s *Service) CreateReplay(ctx context.Context, request CreateReplayRequest) (Replay, error) {
	if !validOwner(request.SourceOwnerType, false) || strings.TrimSpace(request.SourceOwnerID) == "" || request.ModelConfigID < 1 {
		return Replay{}, ErrInvalidRequest
	}
	state, err := s.repository.GetRunState(ctx, request.SourceOwnerType, request.SourceOwnerID)
	if err != nil {
		return Replay{}, err
	}
	if state.Status != StatusComplete {
		return Replay{}, fmt.Errorf("%w: source audit is %s", ErrImmutable, state.Status)
	}
	payloads, err := s.repository.ListPayloads(ctx, request.SourceOwnerType, request.SourceOwnerID)
	if err != nil {
		return Replay{}, err
	}
	if len(payloads) == 0 {
		return Replay{}, fmt.Errorf("%w: source audit has no model payload", ErrInvalidRequest)
	}
	var cutoff time.Time
	for _, payload := range payloads {
		if payload.CutoffAt != nil {
			if cutoff.IsZero() || payload.CutoffAt.Before(cutoff) {
				cutoff = payload.CutoffAt.UTC()
			}
		}
	}
	if cutoff.IsZero() {
		return Replay{}, fmt.Errorf("%w: source audit cutoff is unavailable", ErrInvalidRequest)
	}
	replay := Replay{SourceOwnerType: request.SourceOwnerType, SourceOwnerID: strings.TrimSpace(request.SourceOwnerID), ModelConfigID: request.ModelConfigID, Status: "queued", CutoffAt: cutoff}
	if err = s.repository.CreateReplay(ctx, &replay); err != nil {
		return Replay{}, err
	}
	if _, err = s.repository.BeginRun(ctx, OwnerReplay, replay.ReplayID); err != nil {
		return Replay{}, err
	}
	return replay, nil
}

type replayOutput struct {
	CallSequence int    `json:"callSequence"`
	Phase        string `json:"phase"`
	Content      string `json:"content"`
	SHA256       string `json:"sha256"`
}

func (s *Service) ExecuteReplay(ctx context.Context, replayID string, executor ReplayExecutor) (ReplayView, error) {
	if executor == nil {
		return ReplayView{}, ErrInvalidRequest
	}
	replay, err := s.repository.GetReplay(ctx, replayID)
	if err != nil {
		return ReplayView{}, err
	}
	if err = s.repository.UpdateReplayStatus(ctx, replayID, "queued", "running", ""); err != nil {
		return ReplayView{}, err
	}
	fail := func(cause error) (ReplayView, error) {
		message, _ := RedactText(cause.Error())
		_ = s.repository.UpdateReplayStatus(context.WithoutCancel(ctx), replayID, "running", "failed", message)
		_ = s.recorder.Fail(context.WithoutCancel(ctx), OwnerReplay, replayID, cause)
		view, _ := s.GetReplay(context.WithoutCancel(ctx), replayID)
		return view, cause
	}
	sourceRows, err := s.repository.ListPayloads(ctx, replay.SourceOwnerType, replay.SourceOwnerID)
	if err != nil {
		return fail(err)
	}
	sort.Slice(sourceRows, func(i, j int) bool {
		if sourceRows[i].CallSequence == sourceRows[j].CallSequence {
			return sourceRows[i].Attempt < sourceRows[j].Attempt
		}
		return sourceRows[i].CallSequence < sourceRows[j].CallSequence
	})
	outputs := make([]replayOutput, 0, len(sourceRows))
	diffs := make([]map[string]any, 0, len(sourceRows))
	for sequence, source := range sourceRows {
		decoded, decodeErr := decodePayload(source)
		if decodeErr != nil {
			return fail(decodeErr)
		}
		prepared, prepareErr := s.recorder.Prepare(ctx, CallInput{OwnerType: OwnerReplay, OwnerID: replayID, Phase: source.Phase, CallSequence: sequence + 1, Attempt: 1, ModelParameters: map[string]any{"modelConfigId": replay.ModelConfigID, "sourcePayloadId": source.PayloadID}, CutoffAt: &replay.CutoffAt, Prompt: decoded.FinalPrompt, Evidence: json.RawMessage(decoded.Evidence), Tools: decodeStringList(source.ToolsJSON)})
		if prepareErr != nil {
			return fail(prepareErr)
		}
		callResult, callErr := executor.CompleteReplay(ctx, ReplayCall{Phase: source.Phase, Prompt: prepared.Prompt, Evidence: decoded.Evidence, ModelConfigID: replay.ModelConfigID})
		attemptLog, _ := json.Marshal(callResult.AttemptLog)
		if recordErr := s.recorder.Record(context.WithoutCancel(ctx), prepared, CallResult{RawResponse: callResult.Content, ProviderName: callResult.ProviderName, ModelName: callResult.ModelName, RepairLog: string(attemptLog), ModelParameters: callResult.ModelParameters}); recordErr != nil {
			return fail(recordErr)
		}
		if callErr != nil {
			return fail(callErr)
		}
		redacted, _ := RedactText(callResult.Content)
		_, digest, digestErr := encodeGZIP(redacted)
		if digestErr != nil {
			return fail(digestErr)
		}
		originalHash := deref(source.RawResponseSHA256)
		if originalHash == "" {
			originalHash = deref(source.RepairedResponseSHA256)
		}
		outputs = append(outputs, replayOutput{CallSequence: sequence + 1, Phase: source.Phase, Content: redacted, SHA256: digest})
		diffs = append(diffs, map[string]any{"callSequence": sequence + 1, "sourceSha256": originalHash, "replaySha256": digest, "changed": originalHash != digest})
	}
	resultJSON, err := json.Marshal(outputs)
	if err != nil {
		return fail(err)
	}
	diffJSON, err := json.Marshal(map[string]any{"calls": diffs, "changedCount": countChanged(diffs)})
	if err != nil {
		return fail(err)
	}
	resultBlob, resultHash, err := encodeGZIP(string(resultJSON))
	if err != nil {
		return fail(err)
	}
	if err = s.repository.InsertReplayResult(ctx, &ReplayResult{ReplayID: replayID, ResultCodec: "gzip", ResultBlob: resultBlob, ResultSHA256: resultHash, DiffSummaryJSON: string(diffJSON)}); err != nil {
		return fail(err)
	}
	if err = s.recorder.Complete(ctx, OwnerReplay, replayID); err != nil {
		return fail(err)
	}
	if err = s.repository.UpdateReplayStatus(ctx, replayID, "running", "completed", ""); err != nil {
		return ReplayView{}, err
	}
	return s.GetReplay(ctx, replayID)
}

func decodeStringList(value string) []string {
	var result []string
	if json.Unmarshal([]byte(value), &result) != nil || result == nil {
		return []string{}
	}
	return result
}

func countChanged(values []map[string]any) int {
	count := 0
	for _, value := range values {
		if changed, _ := value["changed"].(bool); changed {
			count++
		}
	}
	return count
}

func (s *Service) GetReplay(ctx context.Context, replayID string) (ReplayView, error) {
	replay, err := s.repository.GetReplay(ctx, strings.TrimSpace(replayID))
	if err != nil {
		return ReplayView{}, err
	}
	view := ReplayView{Replay: replay}
	result, resultErr := s.repository.GetReplayResult(ctx, replayID)
	if resultErr == nil {
		decoded, decodeErr := decodeGZIP(result.ResultBlob, result.ResultSHA256)
		if decodeErr != nil {
			return ReplayView{}, decodeErr
		}
		view.Result = &DecodedResult{ReplayResult: result, Result: decoded}
	} else if !errors.Is(resultErr, ErrNotFound) {
		return ReplayView{}, resultErr
	}
	return view, nil
}
