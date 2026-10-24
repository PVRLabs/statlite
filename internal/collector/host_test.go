package collector

import "testing"

func TestCPUUsageForInterval(t *testing.T) {
	tests := []struct {
		name      string
		total     uint64
		idle      uint64
		wantUsage float64
		wantValue bool
		wantError bool
	}{
		{name: "no clock tick yet"},
		{name: "idle advances without total", idle: 1, wantError: true},
		{name: "valid interval", total: 10, idle: 7, wantUsage: 0.3, wantValue: true},
		{name: "idle exceeds total", total: 2, idle: 3, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := cpuUsageForInterval(test.total, test.idle)
			if (err != nil) != test.wantError {
				t.Fatalf("cpuUsageForInterval() error = %v, wantError %v", err, test.wantError)
			}
			if !test.wantValue {
				if got != nil {
					t.Fatalf("cpuUsageForInterval() = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != test.wantUsage {
				t.Fatalf("cpuUsageForInterval() = %v, want %v", got, test.wantUsage)
			}
		})
	}
}

func TestCPUUsageFromCountersResetsBaselineAfterRollback(t *testing.T) {
	tests := []struct {
		name      string
		before    [2]uint64
		rollback  [2]uint64
		after     [2]uint64
		wantUsage float64
	}{
		{name: "total rollback", before: [2]uint64{100, 70}, rollback: [2]uint64{10, 7}, after: [2]uint64{15, 10}, wantUsage: 0.4},
		{name: "idle rollback", before: [2]uint64{100, 70}, rollback: [2]uint64{110, 5}, after: [2]uint64{120, 9}, wantUsage: 0.6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sampler := NewHostSampler()
			if usage, err := sampler.cpuUsageFromCounters(test.before[0], test.before[1]); err != nil || usage != nil {
				t.Fatalf("first sample = %v, %v; want nil, nil", usage, err)
			}
			if usage, err := sampler.cpuUsageFromCounters(test.rollback[0], test.rollback[1]); err != nil || usage != nil {
				t.Fatalf("rollback sample = %v, %v; want nil, nil", usage, err)
			}
			usage, err := sampler.cpuUsageFromCounters(test.after[0], test.after[1])
			if err != nil || usage == nil || *usage != test.wantUsage {
				t.Fatalf("sample after rollback = %v, %v; want %v, nil", usage, err, test.wantUsage)
			}
		})
	}
}
