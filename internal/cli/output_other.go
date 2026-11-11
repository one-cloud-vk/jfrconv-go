// SPDX-License-Identifier: Apache-2.0
//go:build !darwin && !linux

package cli

import (
	"fmt"
	"os"
)

func duplicateDescriptor(fd int, name string) (*os.File, error) {
	return nil, fmt.Errorf("descriptor output is supported only on Linux and macOS")
}
