// SPDX-License-Identifier: Apache-2.0
//go:build darwin || linux

package cli

import (
	"os"
	"syscall"
)

func duplicateDescriptor(fd int, name string) (*os.File, error) {
	// F_DUPFD_CLOEXEC preserves the original open file description and offset.
	duplicate, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_DUPFD_CLOEXEC, 3)
	if errno != 0 {
		return nil, errno
	}
	f := os.NewFile(duplicate, name)
	if f == nil {
		syscall.Close(int(duplicate))
		return nil, syscall.EBADF
	}
	return f, nil
}
