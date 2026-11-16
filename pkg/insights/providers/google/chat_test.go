package google

import (
	"encoding/json"
	"testing"
)

func TestFunctionArgsJSON(t *testing.T) {
	if got := functionArgsJSON(nil); got != "{}" {
		t.Errorf("functionArgsJSON(nil) = %q, want {}", got)
	}

	got := functionArgsJSON(map[string]any{"topic": "fractions", "count": 3})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("functionArgsJSON returned invalid JSON %q: %v", got, err)
	}
	if decoded["topic"] != "fractions" || decoded["count"] != float64(3) {
		t.Errorf("functionArgsJSON = %q, want the model's arguments", got)
	}
}
