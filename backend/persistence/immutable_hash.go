package persistence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"go-stock/backend/models"
)

var timeType = reflect.TypeOf(time.Time{})

// SealStrategySnapshotBundle canonicalizes every immutable business record and
// replaces SnapshotHash with the SHA-256 digest of that complete record. It is
// intentionally explicit: callers seal once, then AppendStrategySnapshotBundle
// verifies the seal instead of trusting a merely non-empty hash.
func SealStrategySnapshotBundle(bundle *StrategySnapshotBundle) error {
	if bundle == nil {
		return fmt.Errorf("%w: snapshot bundle is nil", ErrInvalidImmutableRecord)
	}
	bundle.Run.CandidateCount = len(bundle.Candidates)
	bundle.Run.RuleCount = len(bundle.Rules)
	bundle.Run.OrderEventCount = len(bundle.OrderEvents)
	bundle.Run.SecuritySnapshotCount = len(bundle.SecurityMaster)
	bundle.Run.CorporateActionCount = len(bundle.CorporateActions)

	for i := range bundle.Candidates {
		if err := sealSnapshotRecord(&bundle.Candidates[i]); err != nil {
			return fmt.Errorf("seal candidate %d: %w", i, err)
		}
	}
	for i := range bundle.Rules {
		if err := sealSnapshotRecord(&bundle.Rules[i]); err != nil {
			return fmt.Errorf("seal rule %d: %w", i, err)
		}
	}
	for i := range bundle.OrderEvents {
		if err := sealSnapshotRecord(&bundle.OrderEvents[i]); err != nil {
			return fmt.Errorf("seal order event %d: %w", i, err)
		}
	}
	for i := range bundle.SecurityMaster {
		if err := sealSnapshotRecord(&bundle.SecurityMaster[i]); err != nil {
			return fmt.Errorf("seal security master %d: %w", i, err)
		}
	}
	for i := range bundle.CorporateActions {
		if err := sealSnapshotRecord(&bundle.CorporateActions[i]); err != nil {
			return fmt.Errorf("seal corporate action %d: %w", i, err)
		}
	}
	if err := sealSnapshotRecord(&bundle.Run); err != nil {
		return fmt.Errorf("seal run: %w", err)
	}
	return nil
}

// SealStrategyOrderEvents canonicalizes lifecycle events before append.
func SealStrategyOrderEvents(events []models.OrderEvent) error {
	for i := range events {
		if err := sealSnapshotRecord(&events[i]); err != nil {
			return fmt.Errorf("seal order event %d: %w", i, err)
		}
	}
	return nil
}

// VerifyStrategyOrderEvents revalidates immutable event seals without
// replaying or mutating the ledger. Read-only portfolio adapters use this
// before exposing events as an accounting source.
func VerifyStrategyOrderEvents(events []models.OrderEvent) error {
	for i := range events {
		if err := verifySnapshotRecord(events[i]); err != nil {
			return fmt.Errorf("verify order event %d: %w", i, err)
		}
	}
	return nil
}

func sealSnapshotRecord(record any) error {
	canonical, normalized, err := canonicalSnapshotRecord(record)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	hash := hex.EncodeToString(digest[:])
	switch target := record.(type) {
	case *models.StrategyRunSnapshot:
		*target = normalized.(models.StrategyRunSnapshot)
		target.SnapshotHash = hash
	case *models.CandidateSnapshot:
		*target = normalized.(models.CandidateSnapshot)
		target.SnapshotHash = hash
	case *models.RuleSnapshot:
		*target = normalized.(models.RuleSnapshot)
		target.SnapshotHash = hash
	case *models.OrderEvent:
		*target = normalized.(models.OrderEvent)
		target.SnapshotHash = hash
	case *models.SecurityMasterHistory:
		*target = normalized.(models.SecurityMasterHistory)
		target.SnapshotHash = hash
	case *models.CorporateActionEvent:
		*target = normalized.(models.CorporateActionEvent)
		target.SnapshotHash = hash
	case *models.Trade:
		*target = normalized.(models.Trade)
		target.SnapshotHash = hash
	default:
		return fmt.Errorf("%w: unsupported snapshot type %T", ErrInvalidImmutableRecord, record)
	}
	return nil
}

func verifySnapshotRecord(record any) error {
	canonical, normalized, err := canonicalSnapshotRecord(record)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	want := hex.EncodeToString(digest[:])
	var got string
	var originalPayload string
	var normalizedPayload string
	switch row := record.(type) {
	case models.StrategyRunSnapshot:
		got, originalPayload, normalizedPayload = row.SnapshotHash, row.PayloadJSON, normalized.(models.StrategyRunSnapshot).PayloadJSON
	case models.CandidateSnapshot:
		got, originalPayload, normalizedPayload = row.SnapshotHash, row.PayloadJSON, normalized.(models.CandidateSnapshot).PayloadJSON
	case models.RuleSnapshot:
		got, originalPayload, normalizedPayload = row.SnapshotHash, row.PayloadJSON, normalized.(models.RuleSnapshot).PayloadJSON
	case models.OrderEvent:
		got, originalPayload, normalizedPayload = row.SnapshotHash, row.PayloadJSON, normalized.(models.OrderEvent).PayloadJSON
	case models.SecurityMasterHistory:
		got, originalPayload, normalizedPayload = row.SnapshotHash, row.PayloadJSON, normalized.(models.SecurityMasterHistory).PayloadJSON
	case models.CorporateActionEvent:
		got, originalPayload, normalizedPayload = row.SnapshotHash, row.PayloadJSON, normalized.(models.CorporateActionEvent).PayloadJSON
	case models.Trade:
		got, originalPayload, normalizedPayload = row.SnapshotHash, row.PayloadJSON, normalized.(models.Trade).PayloadJSON
	default:
		return fmt.Errorf("%w: unsupported snapshot type %T", ErrInvalidImmutableRecord, record)
	}
	if strings.TrimSpace(got) != got || len(got) != sha256.Size*2 || got != strings.ToLower(got) || got != want {
		return fmt.Errorf("%w: snapshot SHA-256 mismatch", ErrInvalidImmutableRecord)
	}
	if originalPayload != normalizedPayload || !snapshotRecordIsCanonical(record, normalized) {
		return fmt.Errorf("%w: immutable business fields or payload JSON are not canonical", ErrInvalidImmutableRecord)
	}
	return nil
}

func snapshotRecordIsCanonical(record, normalized any) bool {
	clearInfrastructure := func(value any) any {
		switch row := value.(type) {
		case models.StrategyRunSnapshot:
			row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
			return row
		case models.CandidateSnapshot:
			row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
			return row
		case models.RuleSnapshot:
			row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
			return row
		case models.OrderEvent:
			row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
			return row
		case models.SecurityMasterHistory:
			row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
			return row
		case models.CorporateActionEvent:
			row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
			return row
		case models.Trade:
			row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
			return row
		default:
			return value
		}
	}
	return reflect.DeepEqual(clearInfrastructure(record), clearInfrastructure(normalized))
}

func canonicalSnapshotRecord(record any) ([]byte, any, error) {
	var normalized any
	switch row := record.(type) {
	case *models.StrategyRunSnapshot:
		normalized = *row
	case models.StrategyRunSnapshot:
		normalized = row
	case *models.CandidateSnapshot:
		normalized = *row
	case models.CandidateSnapshot:
		normalized = row
	case *models.RuleSnapshot:
		normalized = *row
	case models.RuleSnapshot:
		normalized = row
	case *models.OrderEvent:
		normalized = *row
	case models.OrderEvent:
		normalized = row
	case *models.SecurityMasterHistory:
		normalized = *row
	case models.SecurityMasterHistory:
		normalized = row
	case *models.CorporateActionEvent:
		normalized = *row
	case models.CorporateActionEvent:
		normalized = row
	case *models.Trade:
		normalized = *row
	case models.Trade:
		normalized = row
	default:
		return nil, nil, fmt.Errorf("%w: unsupported snapshot type %T", ErrInvalidImmutableRecord, record)
	}

	// Normalize a concrete pointer, then retrieve it again.
	switch row := normalized.(type) {
	case models.StrategyRunSnapshot:
		normalizeImmutableValue(reflect.ValueOf(&row).Elem())
		row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
		payload, err := canonicalJSON(row.PayloadJSON)
		if err != nil {
			return nil, nil, err
		}
		row.PayloadJSON = payload
		normalized = row
	case models.CandidateSnapshot:
		normalizeImmutableValue(reflect.ValueOf(&row).Elem())
		row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
		row.Symbol = strings.ToUpper(row.Symbol)
		row.Decision = strings.ToLower(row.Decision)
		payload, err := canonicalJSON(row.PayloadJSON)
		if err != nil {
			return nil, nil, err
		}
		row.PayloadJSON = payload
		normalized = row
	case models.RuleSnapshot:
		normalizeImmutableValue(reflect.ValueOf(&row).Elem())
		row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
		row.Symbol = strings.ToUpper(row.Symbol)
		row.RuleType = strings.ToLower(row.RuleType)
		row.Path = strings.ToLower(row.Path)
		payload, err := canonicalJSON(row.PayloadJSON)
		if err != nil {
			return nil, nil, err
		}
		row.PayloadJSON = payload
		normalized = row
	case models.OrderEvent:
		normalizeImmutableValue(reflect.ValueOf(&row).Elem())
		row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
		row.Symbol = strings.ToUpper(row.Symbol)
		row.EventType = strings.ToLower(row.EventType)
		payload, err := canonicalJSON(row.PayloadJSON)
		if err != nil {
			return nil, nil, err
		}
		row.PayloadJSON = payload
		normalized = row
	case models.SecurityMasterHistory:
		normalizeImmutableValue(reflect.ValueOf(&row).Elem())
		row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
		row.Symbol = strings.ToUpper(row.Symbol)
		payload, err := canonicalJSON(row.PayloadJSON)
		if err != nil {
			return nil, nil, err
		}
		row.PayloadJSON = payload
		normalized = row
	case models.CorporateActionEvent:
		normalizeImmutableValue(reflect.ValueOf(&row).Elem())
		row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
		row.Symbol = strings.ToUpper(row.Symbol)
		row.ActionType = strings.ToLower(row.ActionType)
		payload, err := canonicalJSON(row.PayloadJSON)
		if err != nil {
			return nil, nil, err
		}
		row.PayloadJSON = payload
		normalized = row
	case models.Trade:
		normalizeImmutableValue(reflect.ValueOf(&row).Elem())
		row.ID, row.CreatedAt, row.SnapshotHash = 0, time.Time{}, ""
		row.Symbol = strings.ToUpper(row.Symbol)
		payload, err := canonicalJSON(row.PayloadJSON)
		if err != nil {
			return nil, nil, err
		}
		row.PayloadJSON = payload
		normalized = row
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: marshal canonical snapshot: %v", ErrInvalidImmutableRecord, err)
	}
	return raw, normalized, nil
}

func normalizeImmutableValue(value reflect.Value) {
	if value.Type() == timeType {
		if value.CanSet() {
			at := value.Interface().(time.Time)
			if !at.IsZero() {
				value.Set(reflect.ValueOf(at.UTC()))
			}
		}
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			normalizeImmutableValue(value.Field(i))
		}
	case reflect.Pointer:
		if !value.IsNil() {
			normalizeImmutableValue(value.Elem())
		}
	case reflect.String:
		if value.CanSet() {
			value.SetString(strings.TrimSpace(value.String()))
		}
	}
}

func canonicalJSON(raw string) (string, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("%w: invalid payload JSON: %v", ErrInvalidImmutableRecord, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("%w: payload JSON contains trailing data", ErrInvalidImmutableRecord)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: canonicalize payload JSON: %v", ErrInvalidImmutableRecord, err)
	}
	return string(canonical), nil
}
