package channelv1

import (
	"encoding/json"
	"testing"
)

func TestModelBudgetPublishClosedShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present bool
		value   any
		valid   bool
	}{
		{"legacy", false, nil, true}, {"zero", true, 0, true}, {"positive", true, 1000, true}, {"max", true, int64(9007199254740991), true},
		{"null", true, nil, false}, {"negative", true, -1, false}, {"fraction", true, 1.5, false}, {"string", true, "1000", false}, {"overflow", true, int64(9007199254740992), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := map[string]any{"public_limited": false, "max_concurrent_runs": 1, "max_runs_per_minute": 5}
			if tc.present {
				quota["max_total_model_tokens"] = tc.value
			}
			raw, _ := json.Marshal(map[string]any{"expected_revision": 0, "definition": map[string]any{"enabled": true, "quota": quota}})
			var out any
			err := Decode("policy-definition-publish.schema.json", raw, &out)
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
