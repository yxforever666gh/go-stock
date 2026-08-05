package data

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestEmbeddedSensitiveWordsIdentity(t *testing.T) {
	if got, want := len(SensitiveWords), 41769; got != want {
		t.Fatalf("sensitive word count = %d, want %d", got, want)
	}
	reconstructed := strings.Join(SensitiveWords, "、")
	if got, want := fmt.Sprintf("%x", sha256.Sum256([]byte(reconstructed))), "8070e44abf1d56bcc9b73b16aee5b07dfab245a0f008420395428f0eb717cb79"; got != want {
		t.Fatalf("sensitive word resource hash = %s, want %s", got, want)
	}
}
