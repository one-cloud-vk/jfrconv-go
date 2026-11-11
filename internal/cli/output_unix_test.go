// SPDX-License-Identifier: Apache-2.0
//go:build darwin || linux

package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestDescriptorCloneRetainsOffsetAndOwnership(t *testing.T) {
	directory := t.TempDir()
	target, err := os.CreateTemp(directory, "capture-")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if _, err := io.WriteString(target, "prefix\n"); err != nil {
		t.Fatal(err)
	}
	alias := "/dev/fd/" + strconv.FormatUint(uint64(target.Fd()), 10)
	before, err := os.Stat(alias)
	if err != nil {
		t.Fatal(err)
	}
	staged := fixtureFile(t, directory, "staged", fakeProfile)
	if err := publishFile(staged, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(target, "after\n"); err != nil {
		t.Fatalf("original descriptor was closed: %v", err)
	}
	after, err := os.Stat(alias)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("descriptor alias was replaced")
	}
	data, err := os.ReadFile(target.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "prefix\n"+fakeProfile+"after\n" {
		t.Fatalf("offset was changed: %q", data)
	}
}

func TestStdoutAliasPreservesRegularFilePrefixInBothModes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skip system descriptor aliases as root; task-owned FIFO remains covered")
	}
	for _, limited := range []bool{false, true} {
		directory := t.TempDir()
		input := fixtureFile(t, directory, "in.jfr", "ok")
		capture, err := os.CreateTemp(directory, "capture-")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(capture, "prefix\n"); err != nil {
			t.Fatal(err)
		}
		args := []string{"-o", "collapsed", input, "/dev/stdout"}
		if limited {
			args = append([]string{"--memory-limit", "32M"}, args...)
		}
		command, cancel := helperCommand(t, args...)
		command.Env = replaceEnvironment(command.Env, map[string]string{helperEnvironment: "append-after"})
		command.Stdout = capture
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		err = command.Run()
		cancel()
		_ = capture.Close()
		if err != nil {
			t.Fatalf("limited=%v: %v %s", limited, err, &diagnostics)
		}
		data, err := os.ReadFile(capture.Name())
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "prefix\n"+fakeProfile+"after\n" {
			t.Fatalf("limited=%v changed prefix/offset: %q", limited, data)
		}
	}
}

func TestNullDeviceIdentityInBothModes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skip direct /dev/null identity probe as root; task-owned FIFO remains covered")
	}
	before, err := os.Stat("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("/dev/null is not a character device")
	}
	for _, limited := range []bool{false, true} {
		directory := t.TempDir()
		input := fixtureFile(t, directory, "in.jfr", "ok")
		args := []string{"-o", "collapsed", input, "/dev/null"}
		if limited {
			args = append([]string{"--memory-limit", "32M"}, args...)
		}
		if code := Main(args, io.Discard, io.Discard, fakeEngine()); code != 0 {
			t.Fatalf("limited=%v exit%d", limited, code)
		}
		after, err := os.Stat("/dev/null")
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before, after) || after.Mode()&os.ModeCharDevice == 0 {
			t.Fatal("device identity/type changed")
		}
	}
}

func TestOwnedFIFOReceivesCompletedResultAndKeepsIdentity(t *testing.T) {
	for _, limited := range []bool{false, true} {
		directory := t.TempDir()
		input := fixtureFile(t, directory, "in.jfr", "ok")
		fifo := filepath.Join(directory, "result.fifo")
		if err := syscall.Mkfifo(fifo, 0600); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(fifo)
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			data []byte
			err  error
		}
		received := make(chan result, 1)
		go func() { data, err := os.ReadFile(fifo); received <- result{data, err} }()
		args := []string{"-o", "collapsed", input, fifo}
		if limited {
			args = append([]string{"--memory-limit", "32M"}, args...)
		}
		command, cancel := helperCommand(t, args...)
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		err = command.Run()
		cancel()
		if err != nil {
			// Release a reader waiting in open after a failed helper, without
			// touching any system device or unowned endpoint.
			fd, openErr := syscall.Open(fifo, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
			if openErr == nil {
				_ = syscall.Close(fd)
			}
			t.Fatalf("limited=%v: %v %s", limited, err, &diagnostics)
		}
		select {
		case got := <-received:
			if got.err != nil || string(got.data) != fakeProfile {
				t.Fatalf("limited=%v got%q %v", limited, got.data, got.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("FIFO reader did not complete")
		}
		after, err := os.Stat(fifo)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before, after) || after.Mode()&os.ModeNamedPipe == 0 {
			t.Fatal("FIFO identity/type changed")
		}
	}
}
