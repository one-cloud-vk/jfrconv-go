// SPDX-License-Identifier: Apache-2.0
//go:build !darwin && !linux

package cli

import "fmt"

func residentBytes(pid int) (uint64, error) {
	return 0, fmt.Errorf("RSS guard is supported only on Linux and macOS")
}
