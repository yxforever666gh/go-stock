package researchaudit

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const redactedValue = "[REDACTED]"

type RedactionManifest struct {
	Fields []string `json:"fields"`
	Count  int      `json:"count"`
}

var sensitiveKeyPattern = regexp.MustCompile(`(?i)(authorization|proxy_authorization|cookie|set_cookie|api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|client[_-]?secret|secret|smtp[_-]?(password|pass))`)
var assignmentPattern = regexp.MustCompile(`(?im)(authorization|proxy-authorization|cookie|set-cookie|api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|client[_-]?secret|secret|smtp[_-]?(?:password|pass))\s*([:=])\s*([^\s,;&\r\n]+)`)
var sensitiveHeaderPattern = regexp.MustCompile(`(?im)^(authorization|proxy-authorization|cookie|set-cookie)\s*:\s*[^\r\n]*`)
var inlineAuthorizationPattern = regexp.MustCompile(`(?i)(authorization|proxy-authorization)\s*:\s*(?:bearer|basic)\s+[^\s,;]+`)
var quotedJSONSecretPattern = regexp.MustCompile(`(?i)"(authorization|proxy[-_]?authorization|cookie|set[-_]?cookie|api[-_]?key|access[-_]?token|refresh[-_]?token|token|password|passwd|client[-_]?secret|secret|smtp[-_]?(?:password|pass))"\s*:\s*"[^"\\]*(?:\\.[^"\\]*)*"`)
var urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

func sensitiveKey(value string) bool {
	return sensitiveKeyPattern.MatchString(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
}

func RedactText(value string) (string, RedactionManifest) {
	fields := make(map[string]struct{})
	trimmed := strings.TrimSpace(value)
	var decoded any
	if trimmed != "" && json.Unmarshal([]byte(trimmed), &decoded) == nil {
		redactJSONValue(decoded, "$", fields)
		if encoded, err := json.Marshal(decoded); err == nil {
			value = string(encoded)
		}
	}
	value = urlPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		query := parsed.Query()
		changed := false
		for key := range query {
			if sensitiveKey(key) {
				query.Set(key, redactedValue)
				fields["query."+strings.ToLower(key)] = struct{}{}
				changed = true
			}
		}
		if !changed {
			return raw
		}
		parsed.RawQuery = query.Encode()
		return parsed.String()
	})
	value = sensitiveHeaderPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := strings.SplitN(match, ":", 2)
		fields["header."+strings.ToLower(strings.ReplaceAll(parts[0], "-", "_"))] = struct{}{}
		return parts[0] + ": " + redactedValue
	})
	value = inlineAuthorizationPattern.ReplaceAllStringFunc(value, func(match string) string {
		separator := strings.Index(match, ":")
		if separator < 0 {
			return match
		}
		name := strings.TrimSpace(match[:separator])
		fields["header."+strings.ToLower(strings.ReplaceAll(name, "-", "_"))] = struct{}{}
		return name + ": " + redactedValue
	})
	value = quotedJSONSecretPattern.ReplaceAllStringFunc(value, func(match string) string {
		separator := strings.Index(match, ":")
		if separator < 0 {
			return match
		}
		key := strings.Trim(strings.TrimSpace(match[:separator]), `"`)
		fields["json."+strings.ToLower(strings.ReplaceAll(key, "-", "_"))] = struct{}{}
		return match[:separator+1] + `"` + redactedValue + `"`
	})
	value = assignmentPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := assignmentPattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		fields["text."+strings.ToLower(strings.ReplaceAll(parts[1], "-", "_"))] = struct{}{}
		return parts[1] + parts[2] + redactedValue
	})
	list := make([]string, 0, len(fields))
	for field := range fields {
		list = append(list, field)
	}
	sort.Strings(list)
	return value, RedactionManifest{Fields: list, Count: len(list)}
}

func redactJSONValue(value any, path string, fields map[string]struct{}) {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			childPath := path + "." + key
			if sensitiveKey(key) {
				item[key] = redactedValue
				fields[childPath] = struct{}{}
				continue
			}
			redactJSONValue(child, childPath, fields)
		}
	case []any:
		for index, child := range item {
			redactJSONValue(child, path+"[]", fields)
			_ = index
		}
	}
}
