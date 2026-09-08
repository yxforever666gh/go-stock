package researchevidence

import (
	"encoding/json"
	"testing"
)

func TestJSONValueEmptyPreservesEnvelopeSemantics(t *testing.T) {
	for _, sample := range []struct {
		payload string
		empty   bool
	}{
		{`null`, true}, {`"  "`, true}, {`[]`, true}, {`{}`, true},
		{`[null,"",[]]`, true}, {`{"code":200,"status":"ok","total":0}`, true},
		{`{"result":{"data":[]},"message":"ok"}`, true},
		{`{"data":null,"other":[1]}`, true},
		{`0`, false}, {`false`, false}, {`[null,0]`, false},
		{`{"result":{"data":[{"price":0}]} }`, false},
		{`{"status":"failed","data":[1]}`, false},
	} {
		t.Run(sample.payload, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(sample.payload), &value); err != nil {
				t.Fatal(err)
			}
			if got := JSONValueEmpty(value); got != sample.empty {
				t.Fatalf("empty=%t want=%t", got, sample.empty)
			}
		})
	}
}
