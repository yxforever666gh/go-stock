package themes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCanonicalEventIdentityIgnoresClaimSpecificFields(t *testing.T) {
	eventAt := time.Date(2026, 8, 28, 2, 30, 10, 0, time.UTC)
	left := CanonicalEventIdentity(" Announcement ", "机器人量产！", eventAt, []string{"SZ000001", "SH600001"})
	right := CanonicalEventIdentity("announcement", "机器人 量产", eventAt.Add(40*time.Second), []string{" sh600001 ", "sz000001", "SZ000001"})
	require.Equal(t, left, right, "source timestamp jitter within a minute and entity ordering must not split one event")

	firstFingerprint := EventFingerprint("theme-robot", "announcement", "机器人量产", "交易所公告版本", eventAt, []string{"SZ000001"})
	secondFingerprint := EventFingerprint("THEME-ROBOT", "announcement", "机器人量产", "媒体摘要版本", eventAt.Add(20*time.Second), []string{"sz000001"})
	require.Equal(t, firstFingerprint, secondFingerprint, "source summaries belong to claims, not event identity")
}

func TestCanonicalEventIdentitySeparatesDifferentEvents(t *testing.T) {
	eventAt := time.Date(2026, 8, 28, 2, 30, 10, 0, time.UTC)
	base := CanonicalEventIdentity("news", "公司进展", eventAt, []string{"SH600001"})
	require.NotEqual(t, base, CanonicalEventIdentity("news", "公司进展", eventAt.Add(time.Minute), []string{"SH600001"}), "a later occurrence is a distinct event")
	require.NotEqual(t, base, CanonicalEventIdentity("news", "公司进展", eventAt, []string{"SZ000002"}), "a different affected entity is a distinct event")
	require.NotEqual(t, base, CanonicalEventIdentity("news", "另一项进展", eventAt, []string{"SH600001"}), "a different title is a distinct event")
}
