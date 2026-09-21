package history

import (
	"context"
	"math"
	"testing"
)

func TestPredicatesRejectNonfiniteAndCancellation(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1)} {
		if ok, err := (Predicate{Kind: PredicateNumeric, Value: value}).Evaluate(context.Background()); ok || err == nil {
			t.Fatal("nonfinite value accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Predicate{Kind: PredicateExitZero}).Evaluate(ctx); err == nil {
		t.Fatal("cancelled evaluation ran")
	}
}
func TestSupersedeDoesNotAliasObjectives(t *testing.T) {
	d := NewDirective("s", "title")
	d.AddObjective("original", Predicate{})
	next := d.Supersede("change")
	next.Objectives[0].Description = "changed"
	if d.Objectives[0].Description != "original" {
		t.Fatal("old version mutated")
	}
}
