package researchevidence

import "strings"

// JSONValueEmpty recognizes empty provider envelopes. It says nothing about
// whether nonempty data is timely, citable, or suitable for either strategy.
func JSONValueEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		if len(typed) == 0 {
			return true
		}
		for _, item := range typed {
			if !JSONValueEmpty(item) {
				return false
			}
		}
		return true
	case map[string]any:
		if len(typed) == 0 {
			return true
		}
		if data, exists := typed["data"]; exists && JSONValueEmpty(data) {
			return true
		}
		for key, item := range typed {
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "code", "rc", "status", "success", "message", "warning", "total", "count", "page", "pagesize":
				continue
			}
			if !JSONValueEmpty(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
