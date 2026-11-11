// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func outputDescriptor(path string) (int, bool) {
	path = filepath.Clean(path)
	switch path {
	case "/dev/stdout":
		return 1, true
	case "/dev/stderr":
		return 2, true
	}
	for _, prefix := range []string{"/dev/fd/", "/proc/self/fd/"} {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		name := strings.TrimPrefix(path, prefix)
		if name == "" {
			return 0, false
		}
		for _, c := range name {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		fd, err := strconv.ParseUint(name, 10, 31)
		if err != nil {
			return 0, false
		}
		return int(fd), true
	}
	return 0, false
}

func streamOutput(path string) bool {
	if _, ok := outputDescriptor(path); ok {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && !info.Mode().IsRegular() && !info.IsDir()
}

func validateStream(info os.FileInfo, descriptor bool) error {
	mode := info.Mode()
	if mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0 {
		return fmt.Errorf("block-device output is unsupported")
	}
	if mode&os.ModeCharDevice != 0 || mode&os.ModeNamedPipe != 0 || descriptor && (mode.IsRegular() || mode&os.ModeSocket != 0) {
		return nil
	}
	if mode&os.ModeSocket != 0 {
		return fmt.Errorf("socket paths are unsupported; use an open descriptor")
	}
	return fmt.Errorf("unsupported special output destination")
}

func validateDestination(path string) error {
	if fd, ok := outputDescriptor(path); ok {
		f, err := duplicateDescriptor(fd, path)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		return validateStream(info, true)
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("output %q is a directory", path)
	}
	if info.Mode().IsRegular() {
		return nil
	}
	return validateStream(info, false)
}

func openStream(path string) (*os.File, error) {
	if fd, ok := outputDescriptor(path); ok {
		f, err := duplicateDescriptor(fd, path)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err == nil {
			err = validateStream(info, true)
		}
		if err != nil {
			f.Close()
			return nil, err
		}
		return f, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if err = validateStream(info, false); err != nil {
		return nil, err
	}
	// Existing-only: never create or truncate a device, pipe or symlink target.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err == nil {
		err = validateStream(info, false)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func createOutputTemp(output, workerDir string) (*os.File, error) {
	directory := workerDir
	if directory == "" && !streamOutput(output) {
		directory = filepath.Dir(output)
	}
	return os.CreateTemp(directory, ".jfrconv-output-")
}

// A regular output is replaced atomically only after the staged file is closed.
// Stream endpoints retain identity and descriptor offsets; publication copies.
func publishFile(staged, output string) error {
	if !streamOutput(output) {
		return os.Rename(staged, output)
	}
	source, err := os.Open(staged)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := openStream(output)
	if err != nil {
		return err
	}
	_, copyErr := io.CopyBuffer(destination, source, make([]byte, 128*1024))
	closeErr := destination.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Remove(staged)
}
