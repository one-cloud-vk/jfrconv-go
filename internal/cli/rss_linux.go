// SPDX-License-Identifier: Apache-2.0
//go:build linux

package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func residentBytes(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				break
			}
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return value * 1024, nil
		}
	}
	return 0, fmt.Errorf("resident memory unavailable for process %d", pid)
}
