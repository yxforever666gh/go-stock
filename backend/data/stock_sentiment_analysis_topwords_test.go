package data

import (
	"go-stock/backend/models"
	"testing"
)

func TestBuildTopWordAnalyzes(t *testing.T) {
	testCases := []struct {
		name        string
		frequencies []models.WordFreqWithWeight
		limit       int
		wantWords   []string
	}{
		{
			name:        "empty_input",
			frequencies: nil,
			limit:       10,
			wantWords:   nil,
		},
		{
			name: "less_than_limit",
			frequencies: []models.WordFreqWithWeight{
				{Word: "b", Frequency: 3},
				{Word: "a", Frequency: 5},
				{Word: "c", Frequency: 1},
			},
			limit:     10,
			wantWords: []string{"a", "b", "c"},
		},
		{
			name: "truncate_to_limit",
			frequencies: []models.WordFreqWithWeight{
				{Word: "a", Frequency: 10},
				{Word: "b", Frequency: 9},
				{Word: "c", Frequency: 8},
				{Word: "d", Frequency: 7},
				{Word: "e", Frequency: 6},
				{Word: "f", Frequency: 5},
				{Word: "g", Frequency: 4},
				{Word: "h", Frequency: 3},
				{Word: "i", Frequency: 2},
				{Word: "j", Frequency: 1},
				{Word: "k", Frequency: 0},
			},
			limit:     10,
			wantWords: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
		},
		{
			name: "non_positive_limit",
			frequencies: []models.WordFreqWithWeight{
				{Word: "a", Frequency: 1},
			},
			limit:     0,
			wantWords: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildTopWordAnalyzes(tc.frequencies, tc.limit)
			if len(got) != len(tc.wantWords) {
				t.Fatalf("len(got)=%d, want=%d", len(got), len(tc.wantWords))
			}
			for i, word := range tc.wantWords {
				if got[i].Word != word {
					t.Fatalf("got[%d]=%q, want=%q", i, got[i].Word, word)
				}
			}
		})
	}
}
