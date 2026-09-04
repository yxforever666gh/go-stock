// Package instruments owns canonical public identifiers for exchange-traded
// instruments. It has no provider, storage, or domain-service dependencies.
package instruments

import (
	"fmt"
	"regexp"
	"strings"
)

var sixDigitInstrument = regexp.MustCompile(`^\d{6}$`)

// InstrumentID is the canonical public identifier shared by charts and
// market-evidence endpoints. Code intentionally keeps the exchange prefix so
// it remains compatible with the existing 2.0 URLs and minute-cache keys.
type InstrumentID struct {
	AssetType string `json:"assetType"`
	Market    string `json:"market"`
	Code      string `json:"code"`
}

// ParseInstrumentID validates the requested asset class and optional market,
// returning a single representation for storage, providers and HTTP output.
func ParseInstrumentID(code, assetType, market string) (InstrumentID, error) {
	assetType = strings.ToLower(strings.TrimSpace(assetType))
	if assetType == "" {
		assetType = "stock"
	}
	normalized, ok := NormalizeInstrumentID(code, assetType)
	if !ok {
		return InstrumentID{}, fmt.Errorf("code does not match assetType %q", assetType)
	}
	resolvedMarket := "SZ"
	if strings.HasPrefix(normalized, "sh") {
		resolvedMarket = "SH"
	}
	market = strings.ToUpper(strings.TrimSpace(market))
	if market != "" && market != resolvedMarket {
		return InstrumentID{}, fmt.Errorf("market %q does not match code %s", market, normalized)
	}
	return InstrumentID{AssetType: assetType, Market: resolvedMarket, Code: normalized}, nil
}

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

// NormalizeETFCode validates and canonicalizes a listed ETF code.
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
