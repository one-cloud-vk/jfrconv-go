// SPDX-License-Identifier: Apache-2.0
//go:build darwin

package cli

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

func residentBytes(pid int) (uint64, error) {
	// Darwin proc_info(PROC_INFO_CALL_PIDINFO, pid, PROC_PIDTASKINFO, 0,
	// proc_taskinfo*, 96). The second uint64 is pti_resident_size.
	// Calling the BSD syscall directly keeps the executable CGO_ENABLED=0.
	var info [96]byte
	n, _, errno := syscall.Syscall6(336, 2, uintptr(pid), 4, 0, uintptr(unsafe.Pointer(&info[0])), uintptr(len(info)))
	runtime.KeepAlive(&info)
	if errno != 0 {
		return 0, errno
	}
	if n != uintptr(len(info)) {
		return 0, fmt.Errorf("incomplete Darwin process memory information")
	}
	return binary.LittleEndian.Uint64(info[8:16]), nil
}
