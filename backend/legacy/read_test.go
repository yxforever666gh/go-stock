package legacy

import "testing"

func TestIsFrozenVersion(t *testing.T) {
	tests := map[string]bool{
		"1.3.6":  true,
		"v1.4.2": true,
		"1.4.3":  false,
		"1.5.0":  false,
		"phase4": false,
		"":       false,
	}
	for version, want := range tests {
		if got := IsFrozenVersion(version); got != want {
			t.Errorf("IsFrozenVersion(%q) = %t, want %t", version, got, want)
		}
	}
}
