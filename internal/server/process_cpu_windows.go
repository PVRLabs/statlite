package server

import "golang.org/x/sys/windows"

func processCPUSeconds() (float64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	// Process durations are counts of 100 ns intervals, not calendar times.
	seconds := func(value windows.Filetime) float64 {
		return float64(uint64(value.HighDateTime)<<32|uint64(value.LowDateTime)) / 10_000_000
	}
	return seconds(kernel) + seconds(user), nil
}
