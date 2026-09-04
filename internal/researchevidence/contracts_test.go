package researchevidence

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSourceDocumentJSONKeepsAuditShapeAndOmitsPromptMetadata(t *testing.T) {
	available := time.Date(2026, 9, 4, 9, 30, 0, 0, time.UTC)
	document := SourceDocument{
		SourceID: "S001", SourceName: "provider", SourceRef: "private-ref", Category: "market",
		CollectedAt: available, AvailableAt: &available, Content: "audit-content", PromptContent: "compact-prompt",
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	for _, expected := range []string{`"sourceId":"S001"`, `"sourceName":"provider"`, `"category":"market"`, `"content":"audit-content"`} {
		if !strings.Contains(value, expected) {
			t.Fatalf("JSON %s does not contain %s", value, expected)
		}
	}
	for _, omitted := range []string{"private-ref", "compact-prompt", "availableAt", `"error"`} {
		if strings.Contains(value, omitted) {
			t.Fatalf("JSON %s leaked omitted value %s", value, omitted)
		}
	}
}

func TestStockCandidateJSONShape(t *testing.T) {
	encoded, err := json.Marshal(StockCandidate{Code: "sh600000", Name: "浦发银行"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"code":"sh600000","name":"浦发银行"}` {
		t.Fatalf("candidate JSON=%s", encoded)
	}
}
