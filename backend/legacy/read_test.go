package legacy

import "testing"

func TestIsFrozenVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{version: "", want: true},
		{version: "phase1-v1", want: true},
		{version: "phase2-v1", want: true},
		{version: "phase3-v2", want: true},
		{version: "phase3-v3", want: true},
		{version: "phase3-v4", want: true},
		{version: "v1.3.2", want: true},
		{version: "1.3.6", want: true},
		{version: "1.4.0", want: true},
		{version: "1.4.1", want: true},
		{version: "1.4.2", want: true},
		{version: " V1.4.2 ", want: true},
		{version: "unknown", want: false},
		{version: "legacy-import", want: false},
		{version: "1.4.3", want: false},
		{version: "1.5.0", want: false},
		{version: "1.6.0", want: false},
		{version: "2.0.0", want: false},
	}
	for _, test := range tests {
		if got := IsFrozenVersion(test.version); got != test.want {
			t.Errorf("IsFrozenVersion(%q) = %t, want %t", test.version, got, test.want)
		}
	}
}

func TestFrozenVersionAliasesReturnsCopy(t *testing.T) {
	aliases := FrozenVersionAliases()
	if len(aliases) == 0 {
		t.Fatal("frozen version aliases are empty")
	}
	aliases[0] = "1.5.0"
	if IsFrozenVersion("1.5.0") {
		t.Fatal("caller mutated the canonical frozen-version boundary")
	}
}
