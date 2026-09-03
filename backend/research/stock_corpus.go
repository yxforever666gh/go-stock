package research

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const (
	defaultStockCandidatePromptBytes = 6 * 1024
	defaultStockBatchPromptBytes     = 64 * 1024
)

type compactStockPromptSource struct {
	SourceID    string          `json:"sourceId"`
	SourceName  string          `json:"sourceName"`
	Status      string          `json:"status"`
	CollectedAt time.Time       `json:"collectedAt"`
	DataAsOf    string          `json:"dataAsOf,omitempty"`
	Content     json.RawMessage `json:"content,omitempty"`
}

type compactStockPromptCandidate struct {
	Code          string                     `json:"code"`
	Name          string                     `json:"name"`
	AsOf          string                     `json:"asOf,omitempty"`
	Order         string                     `json:"order"`
	SourceCount   int                        `json:"sourceCount"`
	IncludedCount int                        `json:"includedCount"`
	Sources       []compactStockPromptSource `json:"sources"`
}

type compactStockPromptBatch struct {
	Order      string            `json:"order"`
	Candidates []json.RawMessage `json:"candidates"`
}

// stockSourceCorpus emits complete JSON records instead of slicing a flat
// source corpus by bytes. Mandatory quote/K-line payloads are packed first;
// optional payloads are admitted only while the per-candidate budget holds.
// Source metadata remains present even when an optional payload is omitted.
func stockSourceCorpus(sources []SourceDocument, candidates []StockCandidate, maxBytes, candidateMaxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = defaultStockBatchPromptBytes
	}
	perCandidate := candidateMaxBytes
	if perCandidate <= 0 {
		perCandidate = defaultStockCandidatePromptBytes
	}
	if len(candidates) > 0 {
		available := (maxBytes - 256) / len(candidates)
		if available > 0 && available < perCandidate {
			perCandidate = available
		}
	}
	batch := compactStockPromptBatch{Order: "candidate_input", Candidates: make([]json.RawMessage, 0, len(candidates))}
	for _, candidate := range candidates {
		encoded := compactCandidateSources(candidate, sources, perCandidate)
		if len(encoded) > 0 {
			batch.Candidates = append(batch.Candidates, encoded)
		}
	}
	encoded, _ := json.Marshal(batch)
	// Per-candidate limits guarantee the normal ten-candidate batch fits. Keep
	// the final guard structural: drop whole candidates, never byte-slice JSON.
	for len(encoded) > maxBytes && len(batch.Candidates) > 0 {
		batch.Candidates = batch.Candidates[:len(batch.Candidates)-1]
		encoded, _ = json.Marshal(batch)
	}
	return string(encoded)
}

func compactCandidateSources(candidate StockCandidate, sources []SourceDocument, maxBytes int) json.RawMessage {
	matched := make([]SourceDocument, 0, 10)
	code, _ := NormalizeMainlandCode(candidate.Code)
	digits := strings.TrimPrefix(strings.TrimPrefix(code, "sh"), "sz")
	for _, source := range sources {
		name := strings.ToLower(source.SourceName)
		if strings.Contains(name, strings.ToLower(code)) || (digits != "" && strings.Contains(name, digits)) {
			matched = append(matched, source)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		left, right := stockSourcePriority(matched[i].SourceName), stockSourcePriority(matched[j].SourceName)
		if left != right {
			return left < right
		}
		return matched[i].SourceID < matched[j].SourceID
	})
	result := compactStockPromptCandidate{Code: code, Name: candidate.Name, Order: "newest_first", Sources: make([]compactStockPromptSource, 0, len(matched))}
	for _, source := range matched {
		status := "ok"
		if source.Error != "" {
			status = "failed"
		}
		dataAsOf := promptDataAsOf(source.PromptContent)
		entry := compactStockPromptSource{SourceID: source.SourceID, SourceName: source.SourceName, Status: status, CollectedAt: source.CollectedAt, DataAsOf: dataAsOf}
		result.Sources = append(result.Sources, entry)
		if status == "ok" && dataAsOf != "" && (stockSourcePriority(source.SourceName) == 0 || result.AsOf == "") {
			result.AsOf = dataAsOf
		}
	}
	result.SourceCount = len(result.Sources)
	for index, source := range matched {
		if source.Error != "" {
			continue
		}
		content := strings.TrimSpace(source.PromptContent)
		if content == "" {
			content = strings.TrimSpace(source.Content)
		}
		if content == "" {
			continue
		}
		var payload json.RawMessage
		if json.Valid([]byte(content)) {
			payload = json.RawMessage(content)
		} else {
			encoded, _ := json.Marshal(content)
			payload = encoded
		}
		trial := result
		trial.Sources = append([]compactStockPromptSource(nil), result.Sources...)
		trial.Sources[index].Content = payload
		trial.IncludedCount = result.IncludedCount + 1
		encoded, _ := json.Marshal(trial)
		if len(encoded) > maxBytes && stockSourcePriority(source.SourceName) <= 2 {
			trial.Sources[index].Content = compactPromptRawJSON(payload, maxBytes/4)
			encoded, _ = json.Marshal(trial)
		}
		if len(encoded) <= maxBytes {
			result = trial
		}
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) <= maxBytes {
		return encoded
	}
	fallback, _ := json.Marshal(compactStockPromptCandidate{Code: code, Name: candidate.Name, Order: "newest_first", SourceCount: len(matched), Sources: []compactStockPromptSource{}})
	return fallback
}

func compactPromptRawJSON(payload json.RawMessage, maxBytes int) json.RawMessage {
	if len(payload) <= maxBytes {
		return payload
	}
	var decoded any
	if json.Unmarshal(payload, &decoded) != nil {
		return json.RawMessage(`{"truncated":true}`)
	}
	for level := 1; level <= 5; level++ {
		candidate := compactPromptNode(decoded, level)
		encoded, _ := json.Marshal(candidate)
		if len(encoded) <= maxBytes {
			return encoded
		}
	}
	return json.RawMessage(`{"truncated":true}`)
}

func compactPromptNode(value any, level int) any {
	arrayLimits := []int{0, 20, 10, 5, 2, 1}
	stringLimits := []int{0, 600, 300, 160, 80, 40}
	switch typed := value.(type) {
	case []any:
		limit := arrayLimits[level]
		if len(typed) > limit {
			typed = typed[:limit]
		}
		result := make([]any, 0, len(typed))
		for _, child := range typed {
			result = append(result, compactPromptNode(child, level))
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = compactPromptNode(child, level)
		}
		return result
	case string:
		return truncateUTF8(typed, stringLimits[level])
	default:
		return value
	}
}

func promptDataAsOf(content string) string {
	if !json.Valid([]byte(content)) {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(content), &payload) != nil {
		return ""
	}
	if value, ok := payload["asOf"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func stockSourcePriority(name string) int {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "实时行情"):
		return 0
	case strings.Contains(name, "日k"):
		return 1
	case strings.Contains(name, "分钟k"):
		return 2
	case strings.Contains(name, "财务"):
		return 3
	case strings.Contains(name, "资金流"):
		return 4
	case strings.Contains(name, "公告"):
		return 5
	case strings.Contains(name, "相关市场新闻"):
		return 6
	case strings.Contains(name, "研报"):
		return 7
	case strings.Contains(name, "概念"):
		return 8
	default:
		return 9
	}
}
