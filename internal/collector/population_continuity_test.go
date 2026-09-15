package collector

import (
	"math"
	"testing"
)

func TestPopulationContinuityAdmitsAdditiveFirstUse(t *testing.T) {
	continuity := newPopulationContinuity(10)
	if _, ok := observeNoError(t, continuity, population("get:200", 4, 0.4)); ok {
		t.Fatal("initial observation produced a delta")
	}

	delta, ok := observeNoError(t, continuity, map[string]populationValue{
		"get:200":  {count: 6, sum: 0.7},
		"post:201": {count: 1, sum: 0.2},
	})
	if !ok {
		t.Fatal("additive first use broke continuity")
	}
	if delta.count != 3 || !closeEnough(delta.sum, 0.5) {
		t.Fatalf("delta = %#v, want count 3 and sum 0.5", delta)
	}
}

func TestPopulationContinuityBreaksForDisappearanceDespiteAggregateGrowth(t *testing.T) {
	continuity := newPopulationContinuity(10)
	observeNoError(t, continuity, map[string]populationValue{
		"get:200":  {count: 10, sum: 1},
		"post:500": {count: 2, sum: 0.5},
	})

	if _, ok := observeNoError(t, continuity, population("get:200", 20, 2)); ok {
		t.Fatal("disappearing tuple produced an aggregate delta")
	}
	delta, ok := observeNoError(t, continuity, population("get:200", 21, 2.1))
	if !ok || delta.count != 1 || !closeEnough(delta.sum, 0.1) {
		t.Fatalf("post-break delta = %#v, %v; want count 1, sum 0.1", delta, ok)
	}
}

func TestPopulationContinuityBreaksForHiddenTupleReset(t *testing.T) {
	continuity := newPopulationContinuity(10)
	observeNoError(t, continuity, map[string]populationValue{
		"get:200":  {count: 10, sum: 1},
		"post:500": {count: 5, sum: 1},
	})

	if _, ok := observeNoError(t, continuity, map[string]populationValue{
		"get:200":  {count: 20, sum: 2},
		"post:500": {count: 1, sum: 0.2},
	}); ok {
		t.Fatal("tuple reset hidden by aggregate growth produced a delta")
	}
}

func TestPopulationContinuityBreaksAcrossMissingOrInvalidObservation(t *testing.T) {
	for _, test := range []struct {
		name      string
		interrupt func(*populationContinuity)
	}{
		{name: "missing population", interrupt: func(c *populationContinuity) { _, _, _ = c.observe(nil) }},
		{name: "invalid scrape", interrupt: func(c *populationContinuity) { c.breakContinuity() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			continuity := newPopulationContinuity(10)
			observeNoError(t, continuity, population("get:200", 4, 0.4))
			test.interrupt(continuity)
			if _, ok := observeNoError(t, continuity, population("get:200", 8, 0.8)); ok {
				t.Fatal("recovery attributed the unobserved interval")
			}
			delta, ok := observeNoError(t, continuity, population("get:200", 9, 0.9))
			if !ok || delta.count != 1 || !closeEnough(delta.sum, 0.1) {
				t.Fatalf("post-recovery delta = %#v, %v; want count 1, sum 0.1", delta, ok)
			}
		})
	}
}

func TestPopulationContinuityBreaksForReplacementAndReturn(t *testing.T) {
	continuity := newPopulationContinuity(10)
	observeNoError(t, continuity, population("get:200", 4, 0.4))
	if _, ok := observeNoError(t, continuity, population("post:201", 2, 0.2)); ok {
		t.Fatal("replacement population produced a delta")
	}
	if _, ok := observeNoError(t, continuity, map[string]populationValue{
		"get:200":  {count: 5, sum: 0.5},
		"post:201": {count: 3, sum: 0.3},
	}); ok {
		t.Fatal("returning tuple produced a delta")
	}
}

func TestPopulationContinuityNewRunForgetsOldTupleIdentities(t *testing.T) {
	continuity := newPopulationContinuity(10)
	observeNoError(t, continuity, population("get:200", 4, 0.4))
	continuity.resetRun()

	if _, ok := observeNoError(t, continuity, population("post:201", 1, 0.1)); ok {
		t.Fatal("first observation in new run produced a delta")
	}
	delta, ok := observeNoError(t, continuity, map[string]populationValue{
		"get:200":  {count: 1, sum: 0.1},
		"post:201": {count: 2, sum: 0.2},
	})
	if !ok {
		t.Fatal("old-run tuple was treated as returning in the new run")
	}
	if delta.count != 2 || !closeEnough(delta.sum, 0.2) {
		t.Fatalf("new-run delta = %#v, want count 2 and sum 0.2", delta)
	}
}

func TestPopulationContinuityBoundsRememberedIdentities(t *testing.T) {
	continuity := newPopulationContinuity(1)
	observeNoError(t, continuity, population("get:200", 1, 0.1))
	if _, _, err := continuity.observe(population("post:201", 1, 0.1)); err != errPopulationIdentityLimit {
		t.Fatalf("identity limit error = %v, want %v", err, errPopulationIdentityLimit)
	}
}

func population(key string, count, sum float64) map[string]populationValue {
	return map[string]populationValue{key: {count: count, sum: sum}}
}

func observeNoError(t *testing.T, continuity *populationContinuity, current map[string]populationValue) (populationValue, bool) {
	t.Helper()
	delta, ok, err := continuity.observe(current)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	return delta, ok
}

func closeEnough(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
}
