// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"jfrconv-go/internal/convert"
)

const helperEnvironment = "_JFRCONV_GO_CLI_TEST_HELPER"
const fakeProfile = "Main.run;Worker.work 6\n"

func fakeEngine() Engine {
	run := func(input string, out io.Writer, format string, options convert.Options) (convert.Stats, error) {
		data, err := os.ReadFile(input)
		if err != nil {
			return convert.Stats{}, err
		}
		switch string(data) {
		case "error":
			_, _ = io.WriteString(out, "partial")
			return convert.Stats{}, errors.New("deliberate conversion failure")
		case "budget":
			file, err := os.CreateTemp(options.TempDir, "owned-spill-")
			if err != nil {
				return convert.Stats{}, err
			}
			_, _ = file.WriteString("scratch")
			_ = file.Close()
			_, _ = io.WriteString(out, "partial")
			return convert.Stats{}, convert.ErrMemoryBudget
		case "rss":
			memory := make([]byte, 64<<20)
			for i := 0; i < len(memory); i += 4096 {
				memory[i] = 1
			}
			time.Sleep(2 * time.Second)
			runtime.KeepAlive(memory)
		}
		_, err = io.WriteString(out, fakeProfile)
		return convert.Stats{Samples: 6, Units: 6}, err
	}
	return Engine{Run: run, Diff: func(base, current string, out io.Writer, format string, options convert.Options) (convert.Stats, error) {
		return run(current, out, format, options)
	}}
}

func TestMain(m *testing.M) {
	if os.Getenv(envWorker) == "1" || os.Getenv(helperEnvironment) != "" {
		engine := fakeEngine()
		if os.Getenv(helperEnvironment) == "real" {
			engine = Engine{Run: convert.Run, Diff: convert.RunDiff}
		}
		code := Main(os.Args[1:], os.Stdout, os.Stderr, engine)
		if code == 0 && os.Getenv(helperEnvironment) == "append-after" && os.Getenv(envWorker) != "1" {
			if _, err := io.WriteString(os.Stdout, "after\n"); err != nil {
				os.Exit(9)
			}
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestActualEngineLimitedBatchReleasesProfiles(t *testing.T) {
	if _, err := residentBytes(os.Getpid()); err != nil {
		t.Skip(err)
	}
	t.Setenv(helperEnvironment, "real")
	directory := t.TempDir()
	outputs := filepath.Join(directory, "outputs")
	if err := os.Mkdir(outputs, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "tests", "fixtures", "mixed.jfr"))
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"--cpu", "--memory-stats", "--memory-limit", "16M", "--max-rss", "32M", "-o", "collapsed"}
	for i := 0; i < 64; i++ {
		arguments = append(arguments, fixture)
	}
	arguments = append(arguments, outputs)
	var diagnostics bytes.Buffer
	engine := Engine{Run: convert.Run, Diff: convert.RunDiff}
	if code := Main(arguments, io.Discard, &diagnostics, engine); code != 0 {
		t.Fatalf("actual limited engine exit %d: %s", code, &diagnostics)
	}
	expression := regexp.MustCompile(`memory: heap_alloc=(\d+) heap_inuse=(\d+) go_retained=(\d+) rss=(\d+) gc=(\d+)`)
	rows := expression.FindAllStringSubmatch(diagnostics.String(), -1)
	if len(rows) != 64 {
		t.Fatalf("expected 64 post-GC observations, got %d: %s", len(rows), &diagnostics)
	}
	var early, late uint64
	for i, row := range rows {
		heap, err := strconv.ParseUint(row[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		rss, err := strconv.ParseUint(row[4], 10, 64)
		if err != nil || rss > 32<<20 {
			t.Fatalf("worker RSS exceeded guard: %s", row[4])
		}
		if i < 8 {
			early += heap
		}
		if i >= len(rows)-8 {
			late += heap
		}
	}
	if late > early+8*(256<<10) {
		t.Fatalf("profiles remain live across a bounded batch: early %d late %d", early/8, late/8)
	}
	data, err := os.ReadFile(filepath.Join(outputs, "mixed.collapsed"))
	if err != nil || !bytes.Contains(data, []byte("demo/Main.run")) {
		t.Fatalf("worker did not run the actual JFR engine: %q %v", data, err)
	}
	var units uint64
	for _, row := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		at := strings.LastIndexByte(row, ' ')
		if at < 0 {
			t.Fatal("invalid output")
		}
		value, err := strconv.ParseUint(row[at+1:], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		units += value
	}
	if units != 6 {
		t.Fatalf("actual output has %d units", units)
	}
	entries, err := os.ReadDir(outputs)
	if err != nil || len(entries) != 1 {
		t.Fatalf("stage scratch survived: %v %v", entries, err)
	}
}

func fixtureFile(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAtomicOutputPreservesExistingFileOnConversionError(t *testing.T) {
	directory := t.TempDir()
	input := fixtureFile(t, directory, "in.jfr", "error")
	output := fixtureFile(t, directory, "out.collapsed", "old")
	var diagnostics bytes.Buffer
	if code := Main([]string{input, output}, io.Discard, &diagnostics, fakeEngine()); code != 1 {
		t.Fatalf("exit%d %s", code, &diagnostics)
	}
	data, _ := os.ReadFile(output)
	if string(data) != "old" {
		t.Fatalf("changed output: %q", data)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 2 {
		t.Fatalf("scratch survived: %v", entries)
	}
	if code := Main([]string{"-o", "collapsed", input, input}, io.Discard, io.Discard, fakeEngine()); code != 1 {
		t.Fatal("accepted input/output alias")
	}
}

func TestLimitedBatchPublishesOnlyAfterAllInputsSucceed(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "later-budget-failure"}[failure], func(t *testing.T) {
			directory := t.TempDir()
			outputs := filepath.Join(directory, "out")
			scratch := filepath.Join(directory, "tmp")
			if err := os.Mkdir(outputs, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(scratch, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", scratch)
			a := fixtureFile(t, directory, "a.cpu.jfr", "ok")
			bContents := "ok"
			if failure {
				bContents = "budget"
			}
			b := fixtureFile(t, directory, "b.wall.jfr", bContents)
			left := fixtureFile(t, outputs, "a.cpu.collapsed", "old-a")
			right := fixtureFile(t, outputs, "b.wall.collapsed", "old-b")
			var diagnostics bytes.Buffer
			code := Main([]string{"--memory-limit", "32M", "-o", "collapsed", a, b, outputs}, io.Discard, &diagnostics, fakeEngine())
			want := 0
			if failure {
				want = LimitExit
			}
			if code != want {
				t.Fatalf("exit%d want%d %s", code, want, &diagnostics)
			}
			for path, old := range map[string]string{left: "old-a", right: "old-b"} {
				data, _ := os.ReadFile(path)
				expected := fakeProfile
				if failure {
					expected = old
				}
				if string(data) != expected {
					t.Fatalf("%s=%q want%q", path, data, expected)
				}
			}
			entries, _ := os.ReadDir(scratch)
			if len(entries) != 0 {
				t.Fatalf("spills survived: %v", entries)
			}
			entries, _ = os.ReadDir(outputs)
			if len(entries) != 2 {
				t.Fatalf("output scratch survived: %v", entries)
			}
		})
	}
}

func TestTinyRSSAndRuntimeBudgetsExit75WithoutChangingOutput(t *testing.T) {
	for _, flags := range [][]string{{"--max-rss", "1"}, {"--memory-limit", "1"}} {
		directory := t.TempDir()
		input := fixtureFile(t, directory, "in.jfr", "ok")
		output := fixtureFile(t, directory, "out.collapsed", "old")
		args := append(append([]string{}, flags...), input, output)
		previous := debug.SetMemoryLimit(-1)
		if code := Main(args, io.Discard, io.Discard, fakeEngine()); code != LimitExit {
			t.Fatalf("%v exit%d", flags, code)
		}
		if limit := debug.SetMemoryLimit(-1); limit != previous {
			t.Fatalf("%v did not restore runtime memory limit: %d, want %d", flags, limit, previous)
		}
		data, _ := os.ReadFile(output)
		if string(data) != "old" {
			t.Fatalf("changed existing output: %q", data)
		}
	}
}

func TestRSSGuardObservesWorkerInsteadOfTreatingGOMEMLIMITAsHardCap(t *testing.T) {
	if _, err := residentBytes(os.Getpid()); err != nil {
		t.Skip(err)
	}
	directory := t.TempDir()
	input := fixtureFile(t, directory, "in.jfr", "rss")
	output := fixtureFile(t, directory, "out.collapsed", "old")
	var diagnostics bytes.Buffer
	code := Main([]string{"--max-rss", "32M", input, output}, io.Discard, &diagnostics, fakeEngine())
	if code != LimitExit {
		t.Fatalf("exit%d %s", code, &diagnostics)
	}
	if !strings.Contains(diagnostics.String(), "resident memory limit exceeded") {
		t.Fatalf("guard did not observe actual RSS: %s", &diagnostics)
	}
	data, _ := os.ReadFile(output)
	if string(data) != "old" {
		t.Fatalf("changed output: %q", data)
	}
}

func TestManifestValidationRejectsUnauthorizedAndSymlinkResults(t *testing.T) {
	directory := t.TempDir()
	destination := fixtureFile(t, directory, "out", "old")
	jobs := []job{{input: "input", output: destination}}
	stats := []convert.Stats{{Samples: 6, Units: 6}}
	if err := writeManifest(directory, jobs, stats); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(destination, filepath.Join(directory, stagedName(0))); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(directory, jobs); err == nil {
		t.Fatal("accepted staged symlink")
	}
	if err := os.Remove(filepath.Join(directory, stagedName(0))); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, directory, stagedName(0), fakeProfile)
	if _, err := readManifest(directory, []job{{output: destination + "x"}}); err == nil {
		t.Fatal("accepted unauthorized destination")
	}
	file, err := os.OpenFile(filepath.Join(directory, manifestName), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("x")
	_ = file.Close()
	if _, err := readManifest(directory, jobs); err == nil {
		t.Fatal("accepted trailing manifest data")
	}
	data, _ := os.ReadFile(destination)
	if string(data) != "old" {
		t.Fatal("validation changed output")
	}
}

func helperCommand(t *testing.T, args ...string) (*exec.Cmd, context.CancelFunc) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(os.Environ(), helperEnvironment+"=main")
	return command, cancel
}
