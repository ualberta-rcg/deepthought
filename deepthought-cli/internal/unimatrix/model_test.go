package unimatrix

import "testing"

func TestLookupKnown(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"qwen35-122b", "gpt-oss-120b", "gpt-oss-20b", "gemma-4-26b-a4b"} {
		m, ok := Lookup(id)
		if !ok {
			t.Errorf("Lookup(%q) = false, want true", id)
			continue
		}
		if m.ID != id {
			t.Errorf("Lookup(%q).ID = %q", id, m.ID)
		}
		if m.Label == "" {
			t.Errorf("Lookup(%q).Label is empty", id)
		}
	}
}

func TestLookupUnknown(t *testing.T) {
	t.Parallel()
	if _, ok := Lookup("nope-999b"); ok {
		t.Fatal("Lookup(unknown) = true, want false")
	}
}

// TestCatalogAllToolCapable guards the catalog's invariant: every entry can drive
// the agent loop. A non-tool model added here would silently break the loop later.
func TestCatalogAllToolCapable(t *testing.T) {
	t.Parallel()
	for _, m := range Catalog() {
		if !m.Agentic() {
			t.Errorf("catalog entry %q is not agentic (chat+tools); catalog is tool-capable only", m.ID)
		}
		if m.Context <= 0 {
			t.Errorf("catalog entry %q has non-positive Context", m.ID)
		}
		if m.Provider != SeedProvider {
			t.Errorf("catalog entry %q attached to %q, want %q", m.ID, m.Provider, SeedProvider)
		}
	}
}

// TestModelCapabilities exercises Can/Agentic derivation.
func TestModelCapabilities(t *testing.T) {
	t.Parallel()
	plain := Model{ID: "x", Capabilities: []Capability{CapChat}}
	if plain.Agentic() {
		t.Error("chat-only model reported agentic")
	}
	full := Model{ID: "y", Capabilities: []Capability{CapChat, CapTools, CapReasoning}}
	if !full.Agentic() || !full.Can(CapReasoning) || full.Can(CapVision) {
		t.Errorf("capability checks wrong for %+v", full)
	}
}

// TestPoolCaches asserts one client per provider name, rebuilt after Drop.
func TestPoolCaches(t *testing.T) {
	t.Parallel()
	p := NewPool()
	a := p.Client("x", "http://one", "k1", "openai")
	b := p.Client("x", "http://one", "k1", "openai")
	if a != b {
		t.Error("pool returned two clients for the same provider")
	}
	p.Drop("x")
	c := p.Client("x", "http://one", "k1", "openai")
	if c == a {
		t.Error("pool kept the stale client after Drop")
	}
	other := p.Client("y", "http://two", "k2", "openai")
	if other == c {
		t.Error("pool conflated two providers")
	}
}

// TestCatalogImmutable asserts Catalog() returns a copy: mutating the result must
// not affect later calls.
func TestCatalogImmutable(t *testing.T) {
	t.Parallel()
	first := Catalog()
	first[0].ID = "tampered"
	again := Catalog()
	if again[0].ID == "tampered" {
		t.Fatal("Catalog() returned shared backing array; mutations leak across calls")
	}
}
