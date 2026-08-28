package themes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

func NormalizeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func NormalizeSourceRef(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port != "" && !((parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443")) {
		host = net.JoinHostPort(host, port)
	}
	parsed.Host = host
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "spm" || lower == "from" || lower == "share_token" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func SourceRefFingerprint(raw string) string {
	return hashText(NormalizeSourceRef(raw))
}

// CanonicalEventIdentity is the single event identity shared by ingestion and
// lifecycle conflict detection. Source summaries and availability timestamps
// deliberately do not participate: they describe a claim about the event, not
// the event itself. A minute bucket tolerates harmless source timestamp jitter
// while still separating repeated same-title events later in the day.
func CanonicalEventIdentity(eventType, title string, eventAt time.Time, entityKeys []string) string {
	keys := append([]string(nil), entityKeys...)
	for i := range keys {
		keys[i] = NormalizeName(keys[i])
	}
	sort.Strings(keys)
	keys = compactNormalizedValues(keys)
	eventMinute := eventAt.UTC().Truncate(time.Minute).Format(time.RFC3339)
	return hashText(strings.Join([]string{NormalizeName(eventType), NormalizeName(title), eventMinute, strings.Join(keys, ",")}, "|"))
}

func EventFingerprint(themeID, eventType, title, summary string, eventAt time.Time, entityKeys []string) string {
	_ = summary // summaries remain claim content and may legitimately differ by source
	return hashText(NormalizeName(themeID) + "|" + CanonicalEventIdentity(eventType, title, eventAt, entityKeys))
}

func compactNormalizedValues(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value == "" || len(result) > 0 && result[len(result)-1] == value {
			continue
		}
		result = append(result, value)
	}
	return result
}

func shanghaiTradeDate(value time.Time) string {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*60*60)
	}
	return value.In(location).Format("2006-01-02")
}

func ClaimFingerprint(summary string) string {
	return hashText(NormalizeName(summary))
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func canonicalSnapshotHash(request FreezeSnapshotRequest) (string, error) {
	type hashConstituent struct {
		AssetType string  `json:"assetType"`
		Market    string  `json:"market"`
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		Role      string  `json:"role"`
		Rank      int     `json:"rank"`
		Score     float64 `json:"score"`
	}
	constituents := make([]hashConstituent, 0, len(request.Constituents))
	for _, item := range request.Constituents {
		constituents = append(constituents, hashConstituent{
			AssetType: strings.ToLower(strings.TrimSpace(item.AssetType)), Market: strings.ToUpper(strings.TrimSpace(item.Market)),
			Code: strings.ToLower(strings.TrimSpace(item.Code)), Name: strings.TrimSpace(item.Name), Role: strings.TrimSpace(item.Role),
			Rank: item.Rank, Score: item.ContributionScore,
		})
	}
	sort.Slice(constituents, func(i, j int) bool {
		left := constituents[i].AssetType + "|" + constituents[i].Market + "|" + constituents[i].Code
		right := constituents[j].AssetType + "|" + constituents[j].Market + "|" + constituents[j].Code
		return left < right
	})
	catalysts := append([]string(nil), request.CatalystIDs...)
	sort.Strings(catalysts)
	payload := struct {
		ThemeID      string            `json:"themeId"`
		TradeDate    string            `json:"tradeDate"`
		CycleNo      int               `json:"cycleNo"`
		Stage        LifecycleStage    `json:"stage"`
		Rank         int               `json:"rank"`
		HeatScore    float64           `json:"heatScore"`
		Summary      string            `json:"summary"`
		ObservedAt   string            `json:"observedAt"`
		Constituents []hashConstituent `json:"constituents"`
		CatalystIDs  []string          `json:"catalystIds"`
	}{request.ThemeID, request.TradeDate, request.CycleNo, request.LifecycleStage, request.Rank, request.HeatScore,
		strings.TrimSpace(request.Summary), request.ObservedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), constituents, catalysts}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return hashText(string(encoded)), nil
}
