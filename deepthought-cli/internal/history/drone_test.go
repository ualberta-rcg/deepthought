package history

import (
	"bytes"
	"testing"
)

func TestDroneCanonicalHashAndResidencyInvariants(t *testing.T) {
	a, err := NewDrone("probe", "session", map[string]any{"b": 2, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDrone("probe", "session", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.BodyHash, b.BodyHash) {
		t.Fatal("canonical bodies should hash identically")
	}
	original := append([]byte(nil), a.Body...)
	if err := a.Demote(StateTombstone); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, a.Body) {
		t.Fatal("demotion destroyed body")
	}
	a.Pinned = true
	if err := a.Demote(StateElided); err == nil {
		t.Fatal("pinned drone was demoted")
	}
}

func TestAnnotationAndSensitivityInvariants(t *testing.T) {
	source, _ := NewDrone("hail", "session", map[string]string{"text": "restricted"})
	source.Sensitivity = SensitivityRestricted
	summary, _ := NewDrone("summary", "session", map[string]string{"text": "small"})
	summary.InheritSensitivity(source)
	if summary.Sensitivity != SensitivityRestricted {
		t.Fatal("derived summary laundered sensitivity")
	}
	if summary.EligibleFor(SensitivityInternal) {
		t.Fatal("fallback relaxed sensitivity")
	}
	annotation, err := NewAnnotation("session", source.ID, "user", "CUDA module caused the OOM")
	if err != nil {
		t.Fatal(err)
	}
	if !annotation.Pinned || annotation.Demote(StateLine) == nil {
		t.Fatal("annotation eviction invariant failed")
	}
}

func TestLegacyCollectiveMigration(t *testing.T) {
	coll := NewCollective(SpawnCollectiveRequest{SessionID: "session", SystemPrompt: "system"})
	inc := coll.StartIncursion("hello")
	tx, _ := inc.AddTransmission("world", nil)
	tx.AddSynapse("thought", "reasoning")
	inc.MarkCompleted()
	drones, err := DronesFromCollective(coll)
	if err != nil {
		t.Fatal(err)
	}
	if len(drones) != 4 {
		t.Fatalf("got %d drones", len(drones))
	}
	if drones[0].ID != coll.ID || drones[1].ID != inc.ID {
		t.Fatal("migration did not preserve IDs")
	}
}
