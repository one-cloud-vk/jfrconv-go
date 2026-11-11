// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"jfrconv-go/internal/convert"
)

func TestArgumentsPreserveSelectionAndSpanFilters(t *testing.T) {
	c, err := Parse([]string{"--cpu", "--wall", "-t", "-s", "r,s", "--latency", "5", "--tag", `\Qrequest-1\E`, "--from", "-200", "--to", "300", "--lines", "--bci", "--norm", "--dot", "--simple", "--classify", "--total", "--highlight", "foo", "--title", "A <b>B</b>", "--grain", "2", "--tail", "10%", "--minwidth", "0.5", "--skip", "1", "-r", "-i", "-o", "collapsed", "--", "-input.jfr", "out.collapsed"})
	if err != nil {
		t.Fatal(err)
	}
	o := c.Options
	if !o.CPU || !o.Wall || !o.Threads || !o.Total || !o.Reverse || !o.Inverted || !o.Classify || !o.HasLatency || !o.HasHighlight || !o.Lines || !o.BCI || !o.Norm || !o.Dot || !o.Simple {
		t.Fatalf("lost flags: %+v", o)
	}
	if o.From != -200 || o.To != 300 || o.Latency != 5 || o.Tail != .1 || o.Skip != 1 || o.Grain != 2 || o.MinWidth != .5 || o.Title != "A <b>B</b>" {
		t.Fatalf("lost values: %+v", o)
	}
	if len(o.State) != 2 || o.State[0] != "R" || o.State[1] != "S" || len(c.Files) != 2 || c.Files[0] != "-input.jfr" {
		t.Fatalf("state/files: %+v", c)
	}
	if !o.Tag.MatchString("request-1") || o.Tag.MatchString("xrequest-1") {
		t.Fatal("tag is not a whole-frame match")
	}
	for _, args := range [][]string{{"--grain", "NaN"}, {"--tail", "2"}, {"--title"}, {"--memory-limit", "0"}, {"--max-rss", "1.5M"}, {"--skip", "-1"}, {"--leak"}, {"--alloc", "--cpu"}, {"--include", "(?=bad)"}} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("accepted invalid arguments %q", args)
		}
	}
	c, err = Parse([]string{"--alloc", "--live", "--nativemem", "--leak"})
	if err != nil || c.Options.Mode != convert.ModeNative {
		t.Fatalf("mode priority: %+v %v", c, err)
	}
}

func TestCheckedMemorySizes(t *testing.T) {
	for text, want := range map[string]uint64{"64MiB": 64 << 20, "2g": 2 << 30, "7": 7, "1KB": 1024} {
		got, err := ParseSize(text)
		if err != nil || got != want {
			t.Fatalf("%q: %d %v", text, got, err)
		}
	}
	for _, text := range []string{"0", "-1", "1.5M", "18446744073709551615G", "12wat", "9223372036854775808"} {
		if _, err := ParseSize(text); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
}

func TestCalendarFractionsAreLiteralMilliseconds(t *testing.T) {
	now := time.Date(2023, 11, 14, 0, 0, 0, 0, time.UTC)
	base, err := parseTimestamp("2023-11-14T22:13:20", now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	for text, delta := range map[string]int64{"2023-11-14T22:13:20.5": 5, "2023-11-14T22:13:20.500": 500, "22:13:20.1000": 1000} {
		got, err := parseTimestamp(text, now, time.UTC)
		if err != nil || got-base != delta {
			t.Fatalf("%q: delta%d %v", text, got-base, err)
		}
	}
	for _, text := range []string{"2023-11-14T25:00:00", "2023-02-30T12:00:00", "22:60:00"} {
		if _, err := parseTimestamp(text, now, time.UTC); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	location, err := time.LoadLocation("America/New_York")
	if err == nil {
		for _, text := range []string{"2023-03-12T02:30:00", "2023-11-05T01:30:00"} {
			if _, err := parseTimestamp(text, now, location); err == nil {
				t.Fatalf("accepted ambiguous/nonexistent %q", text)
			}
		}
	}
}

func TestOutputPlanPreservesDottedNamesAndFormatAliases(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"profile.cpu.jfr", "profile.wall.jfr", ".hidden"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("input"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Parse([]string{"-o", "gz", filepath.Join(directory, "profile.cpu.jfr"), filepath.Join(directory, "profile.wall.jfr"), directory})
	if err != nil {
		t.Fatal(err)
	}
	p, err := makePlan(c)
	if err != nil {
		t.Fatal(err)
	}
	if p.format != "pb.gz" || p.jobs[0].output != filepath.Join(directory, "profile.cpu.pb.gz") || p.jobs[1].output != filepath.Join(directory, "profile.wall.pb.gz") {
		t.Fatalf("bad plan: %+v", p)
	}
	c, err = Parse([]string{filepath.Join(directory, ".hidden"), directory})
	if err != nil {
		t.Fatal(err)
	}
	p, err = makePlan(c)
	if err != nil || p.jobs[0].output != filepath.Join(directory, ".hidden.html") {
		t.Fatalf("hidden file: %+v %v", p, err)
	}
	c, err = Parse([]string{"--diff", "-o", "collapsed", filepath.Join(directory, "profile.cpu.jfr"), filepath.Join(directory, "profile.wall.jfr"), directory})
	if err != nil {
		t.Fatal(err)
	}
	p, err = makePlan(c)
	if err != nil || p.jobs[0].output != filepath.Join(directory, "profile.wall.diff.collapsed") || !p.config.Options.Norm {
		t.Fatalf("diff: %+v %v", p, err)
	}
}
