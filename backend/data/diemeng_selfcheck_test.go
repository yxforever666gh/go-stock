package data

import "testing"

func TestSummarizeDiemengSelfCheckStatus(t *testing.T) {
	tests := []struct {
		name     string
		probes   []DiemengSelfCheckProbe
		expected string
	}{
		{
			name: "all failed",
			probes: []DiemengSelfCheckProbe{
				{Status: "error"},
				{Status: "error"},
			},
			expected: "error",
		},
		{
			name: "partial success",
			probes: []DiemengSelfCheckProbe{
				{Status: "ok"},
				{Status: "error"},
				{Status: "skipped"},
			},
			expected: "degraded",
		},
		{
			name: "success with skipped",
			probes: []DiemengSelfCheckProbe{
				{Status: "ok"},
				{Status: "skipped"},
			},
			expected: "ok",
		},
	}

	for _, tt := range tests {
		if got := summarizeDiemengSelfCheckStatus(tt.probes); got != tt.expected {
			t.Fatalf("%s: expected %s, got %s", tt.name, tt.expected, got)
		}
	}
}

func TestSummarizeDiemengSelfCheckSummary(t *testing.T) {
	probes := []DiemengSelfCheckProbe{
		{Label: "直连", Status: "ok"},
		{Label: "系统代理", Status: "error", Summary: "history TLS handshake timeout"},
		{Label: "应用代理", Status: "skipped", Summary: "代理未配置"},
	}
	got := summarizeDiemengSelfCheckSummary(probes)
	want := "直连可用；系统代理history TLS handshake timeout；应用代理代理未配置"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
