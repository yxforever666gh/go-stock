package data

import (
	"regexp"
	"strings"
)

var sixDigitInstrument = regexp.MustCompile(`^\d{6}$`)

// NormalizeInstrumentID is deliberately separate from the stock-trading
// candidate normalizer: research trading remains stock-only while read-only
// market evidence can address exchange indexes and listed ETFs.
func NormalizeInstrumentID(code, assetType string) (string, bool) {
	assetType = strings.ToLower(strings.TrimSpace(assetType))
	code = strings.ToLower(strings.TrimSpace(code))
	switch assetType {
	case "stock":
		return normalizeStockInstrumentCode(code)
	case "etf":
		return NormalizeETFCode(code)
	case "index":
		return normalizeIndexCode(code)
	default:
		return "", false
	}
}

func normalizeStockInstrumentCode(code string) (string, bool) {
	digits, prefix := splitInstrumentCode(code)
	if !sixDigitInstrument.MatchString(digits) {
		return "", false
	}
	if strings.HasPrefix(digits, "60") || strings.HasPrefix(digits, "68") {
		if prefix != "" && prefix != "sh" {
			return "", false
		}
		return "sh" + digits, true
	}
	if strings.HasPrefix(digits, "00") || strings.HasPrefix(digits, "30") {
		if prefix != "" && prefix != "sz" {
			return "", false
		}
		return "sz" + digits, true
	}
	return "", false
}

func NormalizeETFCode(code string) (string, bool) {
	digits, prefix := splitInstrumentCode(code)
	if !sixDigitInstrument.MatchString(digits) {
		return "", false
	}
	if strings.HasPrefix(digits, "15") {
		if prefix != "" && prefix != "sz" {
			return "", false
		}
		return "sz" + digits, true
	}
	if strings.HasPrefix(digits, "51") || strings.HasPrefix(digits, "56") || strings.HasPrefix(digits, "58") {
		if prefix != "" && prefix != "sh" {
			return "", false
		}
		return "sh" + digits, true
	}
	return "", false
}

func normalizeIndexCode(code string) (string, bool) {
	digits, prefix := splitInstrumentCode(code)
	if !sixDigitInstrument.MatchString(digits) {
		return "", false
	}
	if strings.HasPrefix(digits, "000") {
		if prefix != "" && prefix != "sh" {
			return "", false
		}
		return "sh" + digits, true
	}
	if strings.HasPrefix(digits, "399") {
		if prefix != "" && prefix != "sz" {
			return "", false
		}
		return "sz" + digits, true
	}
	return "", false
}

func splitInstrumentCode(code string) (digits, prefix string) {
	code = strings.ToLower(strings.TrimSpace(code))
	if strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz") {
		return code[2:], code[:2]
	}
	return code, ""
}
