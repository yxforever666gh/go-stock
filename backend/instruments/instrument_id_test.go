package instruments

import "testing"

func TestNormalizeInstrumentID(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		assetType string
		want      string
	}{
		{name: "Shanghai stock", code: "600000", assetType: "stock", want: "sh600000"},
		{name: "Shenzhen stock", code: "sz300001", assetType: "stock", want: "sz300001"},
		{name: "Shanghai ETF", code: "510300", assetType: "etf", want: "sh510300"},
		{name: "Shenzhen ETF", code: "159919", assetType: "etf", want: "sz159919"},
		{name: "Shanghai index", code: "sh000300", assetType: "index", want: "sh000300"},
		{name: "Shenzhen index", code: "399001", assetType: "index", want: "sz399001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := NormalizeInstrumentID(test.code, test.assetType)
			if !ok || got != test.want {
				t.Fatalf("NormalizeInstrumentID(%q, %q) = %q, %v; want %q, true", test.code, test.assetType, got, ok, test.want)
			}
		})
	}
}

func TestNormalizeInstrumentIDRejectsMismatches(t *testing.T) {
	tests := []struct {
		code      string
		assetType string
	}{
		{code: "510300", assetType: "stock"},
		{code: "sh000300", assetType: "stock"},
		{code: "sz600000", assetType: "stock"},
		{code: "sh510300", assetType: "stock"},
		{code: "sz399001", assetType: "stock"},
		{code: "sh159919", assetType: "etf"},
		{code: "600000", assetType: "future"},
		{code: "not-a-code", assetType: "stock"},
	}
	for _, test := range tests {
		if got, ok := NormalizeInstrumentID(test.code, test.assetType); ok {
			t.Errorf("NormalizeInstrumentID(%q, %q) = %q, true; want rejection", test.code, test.assetType, got)
		}
	}
}

func TestParseInstrumentID(t *testing.T) {
	t.Run("defaults asset type and resolves market", func(t *testing.T) {
		got, err := ParseInstrumentID("600000", "", "")
		if err != nil {
			t.Fatal(err)
		}
		want := InstrumentID{AssetType: "stock", Market: "SH", Code: "sh600000"}
		if got != want {
			t.Fatalf("ParseInstrumentID() = %+v; want %+v", got, want)
		}
	})

	t.Run("normalizes request casing", func(t *testing.T) {
		got, err := ParseInstrumentID(" 159915 ", " ETF ", " sz ")
		if err != nil {
			t.Fatal(err)
		}
		want := InstrumentID{AssetType: "etf", Market: "SZ", Code: "sz159915"}
		if got != want {
			t.Fatalf("ParseInstrumentID() = %+v; want %+v", got, want)
		}
	})

	t.Run("rejects mismatched market", func(t *testing.T) {
		if _, err := ParseInstrumentID("sh600000", "stock", "SZ"); err == nil {
			t.Fatal("ParseInstrumentID accepted a mismatched market")
		}
	})
}
