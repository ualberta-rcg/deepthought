package commands

import "testing"

func TestAliasesAndFuzzySearch(t *testing.T) {
	registry := Builtins()
	if got, ok := registry.Lookup("/fish"); !ok || got.Name != "quit" {
		t.Fatalf("fish = %+v ok=%v", got, ok)
	}
	got := registry.Search("/stng", 3)
	if len(got) == 0 || got[0].Name != "settings" {
		t.Fatalf("fuzzy result = %+v", got)
	}
}
