package keyselectors

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestWeightedRandomRejectsEmptyKeys(t *testing.T) {
	if _, err := WeightedRandom(nil, nil, schemas.OpenAI, "gpt-test"); err == nil {
		t.Fatal("expected empty key set to return an error")
	}
}

func TestWeightedRandomUsesZeroWeightKeysAsUniformReserves(t *testing.T) {
	keys := []schemas.Key{
		{ID: "negative", Weight: -1},
		{ID: "zero-first", Weight: 0},
		{ID: "zero-second", Weight: 0},
	}

	first, err := weightedRandomAt(keys, schemas.OpenAI, "gpt-test", 0)
	if err != nil {
		t.Fatalf("select first zero-weight reserve: %v", err)
	}
	if first.ID != "zero-first" {
		t.Fatalf("unit random 0 selected %q, want zero-first", first.ID)
	}

	second, err := weightedRandomAt(keys, schemas.OpenAI, "gpt-test", math.Nextafter(1, 0))
	if err != nil {
		t.Fatalf("select second zero-weight reserve: %v", err)
	}
	if second.ID != "zero-second" {
		t.Fatalf("upper boundary selected %q, want zero-second", second.ID)
	}
}

func TestWeightedRandomIgnoresNegativeWeightsWhenPositiveExists(t *testing.T) {
	keys := []schemas.Key{
		{ID: "negative", Weight: -100},
		{ID: "positive", Weight: 1},
	}
	selected, err := WeightedRandom(nil, keys, schemas.OpenAI, "gpt-test")
	if err != nil {
		t.Fatalf("select key: %v", err)
	}
	if selected.ID != "positive" {
		t.Fatalf("negative-weight key must not participate when a positive weight exists; got %q", selected.ID)
	}
}

func TestWeightedRandomKeepsZeroWeightKeyForRetrySlice(t *testing.T) {
	keys := []schemas.Key{
		{ID: "primary", Weight: 1},
		{ID: "reserve", Weight: 0},
	}

	selected, err := weightedRandomAt(keys, schemas.OpenAI, "gpt-test", 0.5)
	if err != nil || selected.ID != "primary" {
		t.Fatalf("initial selection = %q, %v; want primary", selected.ID, err)
	}

	selected, err = weightedRandomAt(keys[1:], schemas.OpenAI, "gpt-test", 0.5)
	if err != nil || selected.ID != "reserve" {
		t.Fatalf("retry selection = %q, %v; want zero-weight reserve", selected.ID, err)
	}
}

func TestWeightedRandomIgnoresNonFiniteWeightsWhenPositiveExists(t *testing.T) {
	keys := []schemas.Key{
		{ID: "nan", Weight: math.NaN()},
		{ID: "positive-infinity", Weight: math.Inf(1)},
		{ID: "negative-infinity", Weight: math.Inf(-1)},
		{ID: "positive", Weight: 1},
	}

	selected, err := WeightedRandom(nil, keys, schemas.OpenAI, "gpt-test")
	if err != nil {
		t.Fatalf("select key: %v", err)
	}
	if selected.ID != "positive" {
		t.Fatalf("non-finite weights must not participate; got %q", selected.ID)
	}
}

func TestWeightedRandomRejectsOnlyNonFiniteWeights(t *testing.T) {
	keys := []schemas.Key{
		{ID: "nan", Weight: math.NaN()},
		{ID: "infinity", Weight: math.Inf(1)},
	}
	if _, err := WeightedRandom(nil, keys, schemas.OpenAI, "gpt-test"); err == nil {
		t.Fatal("expected a key set without finite positive weights to return an error")
	}
}

func TestWeightedRandomRejectsOnlyNegativeWeights(t *testing.T) {
	keys := []schemas.Key{{ID: "negative", Weight: -1}}
	if _, err := WeightedRandom(nil, keys, schemas.OpenAI, "gpt-test"); err == nil {
		t.Fatal("expected a key set with only negative weights to return an error")
	}
}

func TestWeightedRandomPreservesTinyPositiveWeights(t *testing.T) {
	keys := []schemas.Key{
		{ID: "normal", Weight: 1},
		{ID: "tiny", Weight: 0.001},
	}

	selected, err := weightedRandomAt(keys, schemas.OpenAI, "gpt-test", math.Nextafter(1, 0))
	if err != nil {
		t.Fatalf("select key: %v", err)
	}
	if selected.ID != "tiny" {
		t.Fatalf("tiny positive weight must remain reachable; got %q", selected.ID)
	}
}

func TestWeightedRandomHandlesVeryLargeWeights(t *testing.T) {
	keys := []schemas.Key{
		{ID: "first", Weight: math.MaxFloat64},
		{ID: "second", Weight: math.MaxFloat64},
	}

	selected, err := weightedRandomAt(keys, schemas.OpenAI, "gpt-test", 0.75)
	if err != nil {
		t.Fatalf("select key: %v", err)
	}
	if selected.ID != "second" {
		t.Fatalf("large finite weights must not overflow selection; got %q", selected.ID)
	}
}

func TestWeightedRandomDefensiveUpperBoundaryReturnsLastEligible(t *testing.T) {
	keys := []schemas.Key{
		{ID: "first", Weight: 1},
		{ID: "ignored-zero", Weight: 0},
		{ID: "last-positive", Weight: 1},
	}

	selected, err := weightedRandomAt(keys, schemas.OpenAI, "gpt-test", 1)
	if err != nil {
		t.Fatalf("select defensive boundary: %v", err)
	}
	if selected.ID != "last-positive" {
		t.Fatalf("boundary selected %q, want last-positive", selected.ID)
	}
}

func BenchmarkWeightedRandom(b *testing.B) {
	keys := []schemas.Key{
		{ID: "first", Weight: 0.4},
		{ID: "second", Weight: 0.3},
		{ID: "third", Weight: 0.2},
		{ID: "reserve", Weight: 0},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := WeightedRandom(nil, keys, schemas.OpenAI, "gpt-test"); err != nil {
			b.Fatal(err)
		}
	}
}
