package server

import (
	"math"
	"runtime"
	"testing"
	"time"
)

func TestProcessCPUSecondsWindows(t *testing.T) {
	first, err := processCPUSeconds()
	if err != nil {
		t.Fatal(err)
	}
	second, err := processCPUSeconds()
	if err != nil {
		t.Fatal(err)
	}
	if math.IsNaN(first) || math.IsInf(first, 0) || math.IsNaN(second) || math.IsInf(second, 0) || first < 0 || second < first {
		t.Fatalf("invalid process CPU seconds: %v, %v", first, second)
	}

	// Windows may account CPU time in coarse ticks. Keep working until a tick
	// is recorded, with a generous wall-clock limit for a shared CI runner.
	baseline := second
	deadline := time.Now().Add(5 * time.Second)
	var work uint64 = 1
	for time.Now().Before(deadline) {
		for range 1_000_000 {
			work = work*6364136223846793005 + 1
		}
		runtime.KeepAlive(work)

		current, err := processCPUSeconds()
		if err != nil {
			t.Fatal(err)
		}
		if math.IsNaN(current) || math.IsInf(current, 0) || current < second {
			t.Fatalf("invalid process CPU seconds after workload: %v, previous %v", current, second)
		}
		if current > baseline {
			return
		}
		second = current
	}
	t.Fatalf("process CPU seconds did not increase after CPU workload: %v", baseline)
}
