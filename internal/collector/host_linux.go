//go:build linux

package collector

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func (s *HostSampler) cpuUsage() (*float64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}
	total, idle, err := parseCPUStat(strings.SplitN(string(data), "\n", 2)[0])
	if err != nil {
		return nil, err
	}

	return s.cpuUsageFromCounters(total, idle)
}

func parseCPUStat(line string) (uint64, uint64, error) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("missing aggregate CPU line")
	}
	var total, idle uint64
	for index, field := range fields[1:] {
		if index >= 8 { // guest and guest_nice are already included in user/nice.
			break
		}
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse CPU field %d: %w", index, err)
		}
		total += value
		if index == 3 || index == 4 { // idle and iowait
			idle += value
		}
	}
	return total, idle, nil
}

func hostMemory() (float64, float64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	return parseHostMemory(file)
}

func parseHostMemory(reader io.Reader) (float64, float64, error) {
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	total, totalPresent := values["MemTotal"]
	available, availablePresent := values["MemAvailable"]
	if !totalPresent || !availablePresent || total == 0 || available > total {
		return 0, 0, fmt.Errorf("invalid memory totals")
	}
	return float64(total - available), float64(total), nil
}
