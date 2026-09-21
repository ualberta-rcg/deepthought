package history

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestMutableBodyHashAndVersions(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "history.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, _ := NewDrone("hail", "fixture", map[string]string{"text": "one"})
	if err := s.SaveDrone(context.Background(), d, nil); err != nil {
		t.Fatal(err)
	}
	old := append([]byte(nil), d.BodyHash...)
	d.Body = json.RawMessage(`{"text":"two"}`)
	if err := s.SaveDrone(context.Background(), d, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDrone(context.Background(), d.ID)
	if err != nil || bytes.Equal(old, got.BodyHash) {
		t.Fatalf("stale body hash: %v", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM body_versions WHERE interaction_id=?`, d.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("versions=%d %v", count, err)
	}
}

func TestLegacyImportRemainsResumable(t *testing.T) {
	root := t.TempDir()
	legacy := NewFileStore(filepath.Join(root, "jsonl"))
	c, err := legacy.CreateCollective(SpawnCollectiveRequest{SystemPrompt: "fixture", SessionID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	i := c.StartIncursion("hello")
	tx, _ := i.AddTransmission("world", nil)
	i.MarkCompleted()
	for _, e := range []Entity{i, tx} {
		if err := legacy.SaveObject(e); err != nil {
			t.Fatal(err)
		}
	}
	legacy.Flush()
	s, err := NewSQLiteStore(filepath.Join(root, "history.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.MigrateLegacyChats(filepath.Join(root, "jsonl")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Resume(c.ID)
	if err != nil || len(got.Incursions) != 1 || got.Incursions[0].Transmissions[0].Text != "world" {
		t.Fatalf("import cannot resume: %+v %v", got, err)
	}
}
