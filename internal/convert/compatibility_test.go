// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func fixturePath(name string) string { return filepath.Join("..", "..", "tests", "fixtures", name) }
func canonicalCollapsed(t *testing.T, text string) map[string]uint64 {
	t.Helper()
	m := make(map[string]uint64)
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		at := strings.LastIndexByte(line, ' ')
		if at < 0 {
			t.Fatal(line)
		}
		n, err := strconv.ParseUint(line[at+1:], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		m[line[:at]] += n
	}
	return m
}
func engineOutput(t *testing.T, input, format string, o Options) []byte {
	t.Helper()
	o.TempDir = t.TempDir()
	var out bytes.Buffer
	if _, err := Run(input, &out, format, o); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func goldenOptions(name string) Options {
	o := DefaultOptions()
	switch name {
	case "cpu":
		o.CPU = true
	case "wall":
		o.Wall = true
	case "threads":
		o.CPU = true
		o.Threads = true
	case "wall-threads":
		o.Wall = true
		o.Threads = true
	case "alloc":
		o.Mode = ModeAllocation
	case "alloc-total":
		o.Mode = ModeAllocation
		o.Total = true
	case "alloc-thread-lines":
		o.Mode = ModeAllocation
		o.Threads = true
		o.Lines = true
	case "live":
		o.Mode = ModeLive
		o.Total = true
	case "lock":
		o.Mode = ModeLock
	case "lock-total":
		o.Mode = ModeLock
		o.Total = true
	case "malloc":
		o.Mode = ModeNative
	case "malloc-total":
		o.Mode = ModeNative
		o.Total = true
	case "lines":
		o.Lines = true
	case "bci":
		o.BCI = true
	case "simple":
		o.Simple = true
	case "dot":
		o.Dot = true
	case "norm":
		o.Norm = true
	case "classify":
		o.Classify = true
	case "reverse":
		o.Reverse = true
	case "reverse-thread":
		o.Reverse = true
		o.Threads = true
	case "skip":
		o.Skip = 1
	case "reverse-skip":
		o.Reverse = true
		o.Skip = 1
	case "include":
		o.Include = regexp.MustCompile("demo/Worker.*")
	case "exclude":
		o.Exclude = regexp.MustCompile(".*malloc")
	case "include-exclude":
		o.Include = regexp.MustCompile(".*Main.*")
		o.Exclude = regexp.MustCompile("second/.*")
	case "state":
		o.State = []string{"SLEEPING"}
	case "states":
		o.State = []string{"DEFAULT", "RUNNABLE"}
	case "relative-time":
		o.From = 1000
		o.To = 2000
	case "absolute-time":
		o.From = 1700000001000
	case "negative-time":
		o.From = -1000
	case "minwidth":
		o.MinWidth = 40
	}
	return o
}
func TestOfflineJava41GoldenProfiles(t *testing.T) {
	data, err := os.ReadFile(fixturePath("v41-goldens.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		SourceRevision string `json:"source_revision"`
		Cases          []struct {
			Name, Expected string
			Flags          []string
		}
	}
	if err = json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.SourceRevision != "5930966a92860f6e5d2d89ab6faab5815720bad9" || len(golden.Cases) != 32 {
		t.Fatal("unexpected golden provenance")
	}
	for _, tc := range golden.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			actual := engineOutput(t, fixturePath("mixed.jfr"), "collapsed", goldenOptions(tc.Name))
			if !reflect.DeepEqual(canonicalCollapsed(t, string(actual)), canonicalCollapsed(t, tc.Expected)) {
				t.Fatalf("expected:\n%s\nactual:\n%s", tc.Expected, actual)
			}
		})
	}
}
func TestSpanTagAndClockAcrossChunks(t *testing.T) {
	o := DefaultOptions()
	o.CPU = true
	o.HasLatency = true
	o.Latency = 100
	o.Tag = regexp.MustCompile("cross-request")
	actual := canonicalCollapsed(t, string(engineOutput(t, fixturePath("cross-span.jfr"), "collapsed", o)))
	if want := map[string]uint64{"before/Main.run_[j];before/Worker.work_[j]": 1, "after/Main.run_[j];after/Worker.work_[j]": 1}; !reflect.DeepEqual(actual, want) {
		t.Fatal(actual)
	}
	data, err := os.ReadFile(fixturePath("mixed.jfr"))
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(data); {
		size := int(binary.BigEndian.Uint64(data[offset+8 : offset+16]))
		binary.BigEndian.PutUint64(data[offset+56:offset+64], 10000000)
		offset += size
	}
	path := filepath.Join(t.TempDir(), "clock.jfr")
	os.WriteFile(path, data, 0600)
	o = DefaultOptions()
	o.Mode = ModeLock
	o.Total = true
	var total uint64
	for _, n := range canonicalCollapsed(t, string(engineOutput(t, path, "collapsed", o))) {
		total += n
	}
	if total != 10000000 {
		t.Fatal(total)
	}
}
func TestNativeLeakChronologyKeepsOriginalChunkIdentity(t *testing.T) {
	o := DefaultOptions()
	o.Mode = ModeNative
	o.Leak = true
	o.Tail = 0
	o.Total = true
	actual := canonicalCollapsed(t, string(engineOutput(t, fixturePath("leak-order.jfr"), "collapsed", o)))
	if want := map[string]uint64{"original/Main.run_[j];original/Worker.work_[j]": 4096}; !reflect.DeepEqual(actual, want) {
		t.Fatal(actual)
	}
}
func TestMethodTraceUsesMethodLeafAndEachChunkFrequency(t *testing.T) {
	o := DefaultOptions()
	o.Mode = ModeTrace
	o.Total = true
	actual := canonicalCollapsed(t, string(engineOutput(t, fixturePath("method-trace.jfr"), "collapsed", o)))
	want := map[string]uint64{"demo/Main.run_[j];malloc;demo/Worker.work_[j]": 37500000, "demo/Worker.work_[j]": 12500000}
	if !reflect.DeepEqual(actual, want) {
		t.Fatal(actual)
	}
	o.Total = false
	actual = canonicalCollapsed(t, string(engineOutput(t, fixturePath("method-trace.jfr"), "collapsed", o)))
	for key := range want {
		want[key] = 1
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatal(actual)
	}
}

func TestEnginePreservesEmptySamplesInProtobufAndOmitsCollapsedRows(t *testing.T) {
	o := DefaultOptions()
	o.CPU = true
	input := fixturePath("empty-stacks.jfr")
	actual := canonicalCollapsed(t, string(engineOutput(t, input, "collapsed", o)))
	if !reflect.DeepEqual(actual, map[string]uint64{"demo/Main.run_[j];demo/Worker.work_[j]": 1}) {
		t.Fatal(actual)
	}
	fields := parseWire(t, engineOutput(t, input, "pprof", o))
	total, empty := uint64(0), uint64(0)
	for _, raw := range fields[2] {
		sample := parseWire(t, raw.b)
		amount := wireUint(t, sample, 2)
		total += amount
		if len(sample[1]) == 0 || len(sample[1][0].b) == 0 {
			empty += amount
		}
	}
	if total != 3 || empty != 2 {
		t.Fatal("pprof lost empty samples", total, empty)
	}
	fields = parseWire(t, engineOutput(t, input, "otlp", o))
	resource := parseWire(t, fields[1][0].b)
	scope := parseWire(t, resource[2][0].b)
	total = 0
	empty = 0
	for _, raw := range scope[2] {
		chunk := parseWire(t, raw.b)
		for _, raw := range chunk[2] {
			sample := parseWire(t, raw.b)
			values := packedWire(t, sample[3][0].b)
			total += values[0]
			if wireUint(t, sample, 2) == 0 {
				empty += values[0]
			}
		}
	}
	if total != 3 || empty != 2 {
		t.Fatal("OTLP lost empty samples", total, empty)
	}
}
func TestDifferentialNewFramesHaveZeroBaseAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	base, current := filepath.Join(dir, "base.collapsed"), filepath.Join(dir, "current.collapsed")
	os.WriteFile(base, []byte("A;B 600\nA;C 7\n"), 0600)
	os.WriteFile(current, []byte("A;B 597\nA;C 7\nD 1\n"), 0600)
	var diff bytes.Buffer
	if _, err := RunDiff(base, current, &diff, "collapsed", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(diff.String()), "\n")
	expected := map[string]bool{"A;B 600 597": true, "A;C 7 7": true, "D 0 1": true}
	for _, row := range rows {
		if !expected[row] {
			t.Fatal("incorrect base/current counts", row)
		}
		delete(expected, row)
	}
	if len(expected) != 0 {
		t.Fatal(expected)
	}
	diff.Reset()
	if _, err := RunDiff(base, current, &diff, "html", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	text := diff.String()
	if !strings.Contains(text, "d=-3") || !strings.Contains(text, "d=U") || !strings.Contains(text, "function getDiffColor") {
		t.Fatal("missing original diff data/renderer")
	}
	html := filepath.Join(dir, "difference.html")
	os.WriteFile(html, diff.Bytes(), 0600)
	if actual := canonicalCollapsed(t, string(engineOutput(t, html, "collapsed", DefaultOptions()))); !reflect.DeepEqual(actual, canonicalCollapsed(t, "A;B 597\nA;C 7\nD 1\n")) {
		t.Fatal("differential HTML did not reconstruct current profile", actual)
	}
}
