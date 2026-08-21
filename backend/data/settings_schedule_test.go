package data

import (
	"reflect"
	"testing"
)

func TestNormalizeMinuteProviderOrderPreservesArbitraryOrder(t *testing.T) {
	got, err := NormalizeMinuteProviderOrder([]string{"private", "akshare", "tencent", "sina"}, "public")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"private", "akshare", "tencent", "sina"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v want=%v", got, want)
	}
}

func TestNormalizeMinuteProviderOrderBackfillsLegacyAndMissingRows(t *testing.T) {
	got, err := NormalizeMinuteProviderOrder(nil, "private")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"private", "tencent", "sina", "akshare"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy private order=%v want=%v", got, want)
	}

	got, err = NormalizeMinuteProviderOrder([]string{"sina", "sina"}, "public")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"sina", "tencent", "akshare", "private"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partial order=%v want=%v", got, want)
	}
}

func TestNormalizeAIReviewSchedule(t *testing.T) {
	start, interval, err := NormalizeAIReviewSchedule("", 0)
	if err != nil || start != "09:50" || interval != 15 {
		t.Fatalf("defaults=(%q,%d) err=%v", start, interval, err)
	}
	if _, _, err = NormalizeAIReviewSchedule("09:29", 15); err == nil {
		t.Fatal("start before 09:30 must be rejected")
	}
	if _, _, err = NormalizeAIReviewSchedule("10:00", 4); err == nil {
		t.Fatal("interval below 5 minutes must be rejected")
	}
}
