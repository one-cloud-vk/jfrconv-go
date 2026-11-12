// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"jfrconv-go/internal/convert"
)

func TestSingleMemoryBudgetRunsInCallerAndCleansScratch(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code int
		diff bool
	}{
		{name: "success"},
		{name: "conversion-error", err: errors.New("conversion failed"), code: 1},
		{name: "budget-denial", err: convert.ErrMemoryBudget, code: LimitExit},
		{name: "diff", diff: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			scratch := filepath.Join(directory, "scratch")
			if err := os.Mkdir(scratch, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", scratch)
			input := fixtureFile(t, directory, "input.jfr", "ok")
			output := fixtureFile(t, directory, "output.collapsed", "old")
			const budget = 128 << 20
			previous := debug.SetMemoryLimit(-1)
			called, caller := false, os.Getpid()
			var owned string
			run := func(_ string, out io.Writer, _ string, options convert.Options) (convert.Stats, error) {
				called = true
				if os.Getpid() != caller || os.Getenv(envWorker) == "1" {
					t.Fatal("single memory budget spawned a worker")
				}
				if options.MemoryLimit != budget || debug.SetMemoryLimit(-1) != budget {
					t.Fatal("runtime/application budget was not applied")
				}
				owned = options.TempDir
				if owned == "" || filepath.Dir(owned) != scratch {
					t.Fatalf("scratch is not owned beneath TMPDIR: %q", owned)
				}
				fixtureFile(t, owned, "unfinished-spill", "scratch")
				_, err := io.WriteString(out, fakeProfile)
				if err != nil {
					t.Fatal(err)
				}
				return convert.Stats{Units: 6}, test.err
			}
			engine := Engine{Run: run, Diff: func(base, current string, out io.Writer, format string, options convert.Options) (convert.Stats, error) {
				if base != input {
					t.Fatalf("unexpected diff base: %q", base)
				}
				return run(current, out, format, options)
			}}
			args := []string{"--memory-limit", "128M"}
			if test.diff {
				args = append(args, "--diff", input)
			}
			args = append(args, input, output)
			var diagnostics bytes.Buffer
			if code := Main(args, io.Discard, &diagnostics, engine); code != test.code {
				t.Fatalf("exit %d, want %d: %s", code, test.code, &diagnostics)
			}
			if !called {
				t.Fatal("conversion did not run in the caller")
			}
			if limit := debug.SetMemoryLimit(-1); limit != previous {
				t.Fatalf("runtime limit not restored: %d, want %d", limit, previous)
			}
			if _, err := os.Stat(owned); !os.IsNotExist(err) {
				t.Fatalf("owned scratch survived: %v", err)
			}
			entries, err := os.ReadDir(scratch)
			if err != nil || len(entries) != 0 {
				t.Fatalf("scratch survived: %v %v", entries, err)
			}
			data, err := os.ReadFile(output)
			want := "old"
			if test.code == 0 {
				want = fakeProfile
			}
			if err != nil || string(data) != want {
				t.Fatalf("output %q, want %q: %v", data, want, err)
			}
		})
	}
}

func TestRSSAndBatchLimitsStillUseWorker(t *testing.T) {
	for _, rss := range []bool{false, true} {
		name := "batch"
		if rss {
			name = "rss"
		}
		t.Run(name, func(t *testing.T) {
			if rss {
				if _, err := residentBytes(os.Getpid()); err != nil {
					t.Skip(err)
				}
			}
			directory := t.TempDir()
			input := fixtureFile(t, directory, "input.jfr", "ok")
			outputs := filepath.Join(directory, "outputs")
			if err := os.Mkdir(outputs, 0700); err != nil {
				t.Fatal(err)
			}
			args := []string{"--memory-limit", "32M", "-o", "collapsed", input, input, outputs}
			if rss {
				args = []string{"--max-rss", "64M", "-o", "collapsed", input, outputs}
			}
			called := false
			engine := Engine{Run: func(string, io.Writer, string, convert.Options) (convert.Stats, error) {
				called = true
				return convert.Stats{}, errors.New("worker isolation was bypassed")
			}}
			var diagnostics bytes.Buffer
			if code := Main(args, io.Discard, &diagnostics, engine); code != 0 {
				t.Fatalf("exit %d: %s", code, &diagnostics)
			}
			if called {
				t.Fatal("conversion ran in supervisor")
			}
			data, err := os.ReadFile(filepath.Join(outputs, "input.collapsed"))
			if err != nil || string(data) != fakeProfile {
				t.Fatalf("worker did not produce its result: %q %v", data, err)
			}
		})
	}
}
