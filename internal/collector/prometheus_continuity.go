package collector

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const maxExactPrometheusCount = 1<<53 - 1

func validPrometheusCount(value float64) bool {
	return finiteNonnegative(value) && math.Trunc(value) == value && value <= maxExactPrometheusCount
}

// Include every label and sort by name so pairing never depends on exposition
// order. Quoting keeps tuple components distinct even with embedded delimiters.
func prometheusTupleKey(labels []prometheus.Label) string {
	ordered := slices.Clone(labels)
	slices.SortFunc(ordered, func(a, b prometheus.Label) int { return strings.Compare(a.Name, b.Name) })
	var key strings.Builder
	for _, label := range ordered {
		key.WriteString(strconv.Quote(label.Name))
		key.WriteString(strconv.Quote(label.Value))
	}
	return key.String()
}

type httpRequestTuple struct {
	count float64
	code  int
}

type requestBaseline struct {
	scope    string
	identity float64
	tuples   map[string]httpRequestTuple
}

// A newly observed positive value needs a fresh baseline unless the exporter
// guarantees the tuple started at zero within the interval.
func (previous requestBaseline) breaksContinuity(current requestBaseline, newTuplesStartAtZero bool) bool {
	if previous.tuples == nil || previous.scope != current.scope || previous.identity != current.identity {
		return true
	}
	for key, old := range previous.tuples {
		if next, ok := current.tuples[key]; !ok || next.count < old.count {
			return true
		}
	}
	if newTuplesStartAtZero {
		return false
	}
	for key, next := range current.tuples {
		if _, ok := previous.tuples[key]; !ok && next.count != 0 {
			return true
		}
	}
	return false
}
