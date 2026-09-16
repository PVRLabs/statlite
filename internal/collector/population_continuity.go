package collector

// This file tracks bounded transient source populations without target-specific semantics.

import "errors"

var errPopulationIdentityLimit = errors.New("population identity limit exceeded")

// populationValue is the pair of cumulative values that must describe the
// same source population. Callers own tuple identity and value validation.
type populationValue struct {
	count float64
	sum   float64
}

// populationContinuity keeps only the last bounded source population. It
// returns deltas when every established tuple is still present and has not
// decreased. New tuples are admitted as additive first observations.
//
// A false result establishes the supplied population as a fresh baseline. A
// caller must omit its normalized counters for that observation so persisted
// aggregate counters cannot bridge an unobserved interval.
type populationContinuity struct {
	maxIdentities int
	previous      map[string]populationValue
	seen          map[string]struct{}
}

func newPopulationContinuity(maxIdentities int) *populationContinuity {
	return &populationContinuity{maxIdentities: maxIdentities, seen: make(map[string]struct{})}
}

func (c *populationContinuity) breakContinuity() {
	c.previous = nil
}

// resetRun discards continuity and identity history after an authoritative
// process-run change. A tuple first observed in the new run is therefore
// eligible for additive first use after the new run's baseline is established.
func (c *populationContinuity) resetRun() {
	c.previous = nil
	c.seen = make(map[string]struct{})
}

func (c *populationContinuity) observe(current map[string]populationValue) (populationValue, bool, error) {
	deltas, ok, err := c.observeDeltas(current)
	if err != nil || !ok {
		return populationValue{}, ok, err
	}
	var total populationValue
	for _, delta := range deltas {
		total.count += delta.count
		total.sum += delta.sum
	}
	return total, true, nil
}

// observeDeltas returns each caller-supplied identity's delta. Keeping the
// identity opaque lets exact-contract collectors derive their own subsets
// without teaching continuity tracking about target-specific dimensions.
func (c *populationContinuity) observeDeltas(current map[string]populationValue) (map[string]populationValue, bool, error) {
	if len(current) == 0 {
		c.previous = nil
		return nil, false, nil
	}
	newIdentities := make(map[string]struct{})
	for key := range current {
		if _, ok := c.seen[key]; !ok {
			newIdentities[key] = struct{}{}
		}
	}
	if c.maxIdentities <= 0 || len(c.seen)+len(newIdentities) > c.maxIdentities {
		c.previous = nil
		return nil, false, errPopulationIdentityLimit
	}
	for key := range newIdentities {
		c.seen[key] = struct{}{}
	}
	if len(c.previous) == 0 {
		c.previous = clonePopulation(current)
		return nil, false, nil
	}

	for key, previous := range c.previous {
		value, ok := current[key]
		if !ok || value.count < previous.count || value.sum < previous.sum {
			c.previous = clonePopulation(current)
			return nil, false, nil
		}
	}

	deltas := make(map[string]populationValue, len(current))
	for key, value := range current {
		if previous, ok := c.previous[key]; ok {
			deltas[key] = populationValue{count: value.count - previous.count, sum: value.sum - previous.sum}
			continue
		}
		if _, isNew := newIdentities[key]; !isNew {
			c.previous = clonePopulation(current)
			return nil, false, nil
		}
		deltas[key] = value
	}
	c.previous = clonePopulation(current)
	return deltas, true, nil
}

func clonePopulation(population map[string]populationValue) map[string]populationValue {
	clone := make(map[string]populationValue, len(population))
	for key, value := range population {
		clone[key] = value
	}
	return clone
}
