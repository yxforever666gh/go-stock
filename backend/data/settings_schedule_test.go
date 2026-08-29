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

func TestNormalizeAICapitalDeploymentSettings(t *testing.T) {
	target, buys, minutes, err := NormalizeAICapitalDeploymentSettings(0, 0, 0)
	if err != nil || target != 0.90 || buys != 2 || minutes != 30 {
		t.Fatalf("defaults=(%.2f,%d,%d) err=%v", target, buys, minutes, err)
	}
	target, buys, minutes, err = NormalizeAICapitalDeploymentSettings(0.80, 1, 60)
	if err != nil || target != 0.80 || buys != 1 || minutes != 60 {
		t.Fatalf("safe overrides=(%.2f,%d,%d) err=%v", target, buys, minutes, err)
	}
	for _, test := range []struct {
		target  float64
		buys    int
		minutes int
	}{
		{target: 0.91, buys: 2, minutes: 30},
		{target: 0.90, buys: 3, minutes: 30},
		{target: 0.90, buys: 2, minutes: 4},
	} {
		if _, _, _, err = NormalizeAICapitalDeploymentSettings(test.target, test.buys, test.minutes); err == nil {
			t.Fatalf("unsafe settings unexpectedly accepted: %+v", test)
		}
	}
}
