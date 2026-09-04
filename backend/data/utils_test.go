package data

import (
	"testing"
)

func TestRemoveAllBlankChar(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "spaces", in: "新 希 望", want: "新希望"},
		{name: "mixed whitespace", in: "a\tb\nc\rd", want: "abcd"},
		{name: "empty", in: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RemoveAllBlankChar(tt.in); got != tt.want {
				t.Fatalf("RemoveAllBlankChar(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestStockCodeConversions(t *testing.T) {
	tests := []struct {
		name        string
		stockCode   string
		tushareCode string
	}{
		{name: "shanghai", stockCode: "sh600000", tushareCode: "600000.SH"},
		{name: "shenzhen", stockCode: "sz000802", tushareCode: "000802.SZ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertStockCodeToTushareCode(tt.stockCode); got != tt.tushareCode {
				t.Fatalf("ConvertStockCodeToTushareCode(%q) = %q, want %q", tt.stockCode, got, tt.tushareCode)
			}
			if got := ConvertTushareCodeToStockCode(tt.tushareCode); got != tt.stockCode {
				t.Fatalf("ConvertTushareCodeToStockCode(%q) = %q, want %q", tt.tushareCode, got, tt.stockCode)
			}
		})
	}
}

func TestReplaceSensitiveWords(t *testing.T) {
	original := SensitiveWords
	SensitiveWords = []string{"secret", "敏感"}
	t.Cleanup(func() { SensitiveWords = original })

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "removes all matches", in: "a secret 和敏感内容 secret", want: "a  和内容 "},
		{name: "leaves other text", in: "普通内容", want: "普通内容"},
		{name: "empty", in: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReplaceSensitiveWords(tt.in); got != tt.want {
				t.Fatalf("ReplaceSensitiveWords(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
