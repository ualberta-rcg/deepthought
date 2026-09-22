package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/bubbletea/v2"
	"deepthought-cli/internal/babel"
)

type catalogTestStore struct {
	fakeStore
	client *babel.Client
}

func (s *catalogTestStore) ProviderClient(string) (*babel.Client, error) { return s.client, nil }

func TestManualModelSetupWithoutDiscovery(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(404) }))
	defer endpoint.Close()
	store := &catalogTestStore{client: babel.NewClient(endpoint.URL, "synthetic-test-key")}
	m := NewModelsModel(store).Resize(80, 24)
	provider := m.dirty.Providers[0].Name
	m, _ = m.manualEntry(provider)
	m.edit.input.SetValue("custom/wire-model")
	m, _ = m.Update(keyPress(tea.KeyEnter, ""))
	if store.saved == nil || m.view != mvEdit {
		t.Fatalf("manual entry did not open editor: %s", m.saved)
	}
	last := store.saved.Models[len(store.saved.Models)-1]
	if last.ID != provider+"::custom/wire-model" || last.RequestID() != "custom/wire-model" {
		t.Fatalf("model identity: %+v", last)
	}
	if len(last.Capabilities) != 0 {
		t.Fatal("manual entry invented capabilities")
	}
	if calls.Load() != 0 {
		t.Fatal("manual setup contacted endpoint")
	}
}

func TestUnsupportedCatalogLeavesManualSetupAvailable(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer endpoint.Close()
	store := &catalogTestStore{client: babel.NewClient(endpoint.URL, "synthetic-test-key")}
	m := NewModelsModel(store).Resize(100, 40)
	m, cmd := m.Discover(m.dirty.Providers[0].Name)
	m, _ = m.Update(cmd())
	if !strings.Contains(m.saved, "HTTP 404") {
		t.Fatalf("discovery error lost: %s", m.saved)
	}
	if !strings.Contains(m.View(), "Enter model ID manually") {
		t.Fatal("manual fallback not discoverable")
	}
}
