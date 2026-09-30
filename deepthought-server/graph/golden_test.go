package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestGoldenCollectiveWireContract decodes the shared wire fixture (an
// identical copy lives in deepthought-cli/internal/history/testdata) and checks enum values and that
// re-encoding keeps every field the fixture carries.
func TestGoldenCollectiveWireContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "golden_collective.json"))
	if err != nil {
		t.Fatal(err)
	}
	var coll Collective
	if err := json.Unmarshal(raw, &coll); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if coll.Cycles != 3 {
		t.Fatalf("Collective.Cycles = %d, want 3", coll.Cycles)
	}
	var got []string
	for _, p := range coll.Incursions[0].Transmissions[0].Probes {
		got = append(got, p.Status.String())
	}
	want := []string{"pending", "allowed", "running", "completed", "denied", "failed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("probe statuses = %v, want %v", got, want)
	}
	out, err := json.Marshal(&coll)
	if err != nil {
		t.Fatal(err)
	}
	var fixture, round any
	_ = json.Unmarshal(raw, &fixture)
	_ = json.Unmarshal(out, &round)
	if path := missingField(fixture, round, "$"); path != "" {
		t.Fatalf("re-encoding dropped or changed %s", path)
	}
}

// missingField returns the first path in want that is absent or different in
// got ("" when want is a subset of got).
func missingField(want, got any, path string) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path
		}
		for k, v := range w {
			if p := missingField(v, g[k], path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path
		}
		for i := range w {
			if p := missingField(w[i], g[i], path+"[]"); p != "" {
				return p
			}
		}
	default:
		if !reflect.DeepEqual(want, got) {
			return path
		}
	}
	return ""
}
