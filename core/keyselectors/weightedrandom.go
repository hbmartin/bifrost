package keyselectors

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/maximhq/bifrost/core/schemas"
)

func WeightedRandom(ctx *schemas.BifrostContext, keys []schemas.Key, providerKey schemas.ModelProvider, model string) (schemas.Key, error) {
	if len(keys) == 0 {
		return schemas.Key{}, fmt.Errorf("no keys available for provider %s and model %s", providerKey, model)
	}

	return weightedRandomAt(keys, providerKey, model, rand.Float64())
}

// weightedRandomAt selects a key using unitRandom in [0, 1). It is split from
// WeightedRandom so the boundary behavior can be tested deterministically.
func weightedRandomAt(keys []schemas.Key, providerKey schemas.ModelProvider, model string, unitRandom float64) (schemas.Key, error) {
	if selected, ok := SelectPositiveWeightedAt(keys, func(key *schemas.Key) float64 {
		return key.Weight
	}, unitRandom); ok {
		return selected, nil
	}

	// A zero weight keeps a key out of normal weighted traffic while positive
	// alternatives exist, but it remains a reserve for retry rotation. Preserve
	// the historical all-zero behavior by selecting uniformly among finite zero
	// weights when the current candidate slice contains no positive weight.
	zeroCount := 0
	for _, key := range keys {
		if key.Weight == 0 {
			zeroCount++
		}
	}
	if zeroCount > 0 {
		selectedZero := int(unitRandom * float64(zeroCount))
		if selectedZero < 0 {
			selectedZero = 0
		} else if selectedZero >= zeroCount {
			selectedZero = zeroCount - 1
		}
		for _, key := range keys {
			if key.Weight != 0 {
				continue
			}
			if selectedZero == 0 {
				return key, nil
			}
			selectedZero--
		}
	}

	return schemas.Key{}, fmt.Errorf("no keys with a non-negative finite weight available for provider %s and model %s", providerKey, model)
}

// IsPositiveFiniteWeight reports whether weight can participate in weighted
// selection. NaN is rejected by the positive comparison itself.
func IsPositiveFiniteWeight(weight float64) bool {
	return weight > 0 && !math.IsInf(weight, 0)
}

// SelectPositiveWeightedAt selects an item using its finite, strictly positive
// weight and unitRandom in [0, 1). The normal finite-total path uses two passes
// and no divisions. Normalization is reserved for the exceptional overflow path
// where summing otherwise-valid weights produces positive infinity.
func SelectPositiveWeightedAt[T any](items []T, weightOf func(*T) float64, unitRandom float64) (T, bool) {
	var zero T
	totalWeight := 0.0
	maxWeight := 0.0
	lastEligible := -1
	for i := range items {
		weight := weightOf(&items[i])
		if !IsPositiveFiniteWeight(weight) {
			continue
		}
		totalWeight += weight
		if weight > maxWeight {
			maxWeight = weight
		}
		lastEligible = i
	}
	if lastEligible < 0 {
		return zero, false
	}

	if !math.IsInf(totalWeight, 1) {
		randomValue := unitRandom * totalWeight
		currentWeight := 0.0
		for i := range items {
			weight := weightOf(&items[i])
			if !IsPositiveFiniteWeight(weight) {
				continue
			}
			currentWeight += weight
			if randomValue < currentWeight {
				return items[i], true
			}
		}
		return items[lastEligible], true
	}

	// Extremely large finite weights can overflow their raw sum. Normalize only
	// this exceptional path so the ordinary selector stays division-free.
	totalWeight = 0
	for i := range items {
		weight := weightOf(&items[i])
		if IsPositiveFiniteWeight(weight) {
			totalWeight += weight / maxWeight
		}
	}
	randomValue := unitRandom * totalWeight
	currentWeight := 0.0
	for i := range items {
		weight := weightOf(&items[i])
		if !IsPositiveFiniteWeight(weight) {
			continue
		}
		currentWeight += weight / maxWeight
		if randomValue < currentWeight {
			return items[i], true
		}
	}
	return items[lastEligible], true
}
