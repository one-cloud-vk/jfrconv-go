// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"encoding/binary"
	"io"
	"jfrconv-go/internal/jfr"
	"math"
	"testing"
)

func TestJava45SpanLiteralTagsAndExclusiveEnd(t *testing.T) {
	o := DefaultOptions()
	o.Tag, o.HasTag = "cross.*", true
	filter, err := readSpans(fixturePath("cross-span.jfr"), o)
	if err != nil || len(filter) != 0 {
		t.Fatalf("tag is literal, not a regexp: %v %v", filter, err)
	}
	o.Tag = "cross-request"
	filter, err = readSpans(fixturePath("cross-span.jfr"), o)
	if err != nil || len(filter[1]) != 1 {
		t.Fatalf("literal tag not selected: %v %v", filter, err)
	}
	span := filter[1][0]
	if !filter.contains(1, span.Start) || !filter.contains(1, span.End-1) || filter.contains(1, span.End) || filter.contains(2, span.Start) {
		t.Fatal("interval must include start, exclude end and preserve thread ownership")
	}
	o = DefaultOptions()
	o.LatencyNS = 40_000_000
	filter, err = readSpans(fixturePath("method-trace.jfr"), o)
	if err != nil || len(filter) != 0 {
		t.Fatal("trace threshold must compare ticks at the chunk's frequency", filter, err)
	}
	o.LatencyNS = 37_500_000
	filter, err = readSpans(fixturePath("method-trace.jfr"), o)
	if err != nil || len(filter[1]) != 1 {
		t.Fatal("duration equal to threshold must match", filter, err)
	}
}

func TestJava45CPUTimeNativeLockAndTraceSelection(t *testing.T) {
	o := DefaultOptions()
	o.CPUTime = true
	states, restricted, err := stateIDs(&jfr.Chunk{}, o)
	if err != nil || !restricted || len(states) != 1 || states[0] != 254 {
		t.Fatal(states, restricted, err)
	}
	o.Mode = ModeNativeLock
	if eventMask(o) != jfr.MaskNativeLock || !selected(&jfr.Event{Kind: jfr.NativeLock}, o) || selected(&jfr.Event{Kind: jfr.Lock}, o) || o.ValueType() != "locks" {
		t.Fatal("native locks require their own event class")
	}
	c := jfr.Chunk{TicksPerSec: 2_000_000_000}
	if normalizeValue(75_000_000, &c, o) != 37_500_000 {
		t.Fatal("native lock total must convert ticks to nanoseconds")
	}
	o.Mode = ModeTrace
	if eventMask(o) != jfr.MaskTrace || selected(&jfr.Event{Kind: jfr.Span}, o) || !selected(&jfr.Event{Kind: jfr.MethodTrace}, o) || o.ValueType() != "cpu" || normalizeValue(75_000_000, &c, o) != 75_000_000 {
		t.Fatal("trace selection/value must match 4.5, not the later clock fix")
	}
}

func TestJava45OTLPSpoolRetainsEachEventAndPeriod(t *testing.T) {
	o := DefaultOptions()
	o.CPU = true
	o.TempDir = t.TempDir()
	p, _, err := readProfile(fixturePath("mixed.jfr"), o, "otlp")
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if _, err = p.spool.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var raw [40]byte
	headers, records := 0, 0
	var times []uint64
	for {
		_, err = io.ReadFull(p.spool, raw[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		field := func(index int) uint64 { return binary.LittleEndian.Uint64(raw[index*8:]) }
		if field(1) == math.MaxUint64 {
			headers++
			if field(3) != 10_000_000 {
				t.Fatal("missing period", field(3))
			}
		} else {
			records++
			if field(2) != 1 {
				t.Fatal("OTLP values must remain per-event", field(2))
			}
			times = append(times, field(0))
		}
	}
	if headers != 2 || records != 6 || len(times) < 2 || times[1]-times[0] != 100 {
		t.Fatal("per-event timestamps were aggregated", headers, records, times)
	}
	c := jfr.Chunk{Settings: map[string]string{"interval": "17"}}
	if period, err := otlpPeriod(&c, o); err != nil || period != 17 {
		t.Fatal(period, err)
	}
	c.Settings["interval"] = "0"
	if period, err := otlpPeriod(&c, o); err != nil || period != 10_000_000 {
		t.Fatal(period, err)
	}
	c.Settings["interval"] = "bad"
	if _, err := otlpPeriod(&c, o); err == nil {
		t.Fatal("invalid setting accepted")
	}
}

func TestJava45RawOTLPAllocationsDoNotScaleWithEventCount(t *testing.T) {
	const repetitions = 100
	small := repeatedEventFixture(t, "mixed.jfr", 1)
	large := repeatedEventFixture(t, "mixed.jfr", repetitions)
	o := DefaultOptions()
	o.CPU = true
	o.TempDir = t.TempDir()
	measure := func(input string) (float64, Stats) {
		var stats Stats
		allocations := testing.AllocsPerRun(3, func() {
			var err error
			stats, err = Run(input, io.Discard, "otlp", o)
			if err != nil {
				t.Fatal(err)
			}
		})
		return allocations, stats
	}
	smallAllocations, smallStats := measure(small)
	largeAllocations, largeStats := measure(large)
	if smallStats.Samples == 0 || largeStats.Samples != smallStats.Samples*repetitions || largeStats.Units != smallStats.Units*repetitions {
		t.Fatal("raw OTLP did not process every expanded event", smallStats, largeStats)
	}
	// Bounded sort buffers can grow, but building a thread label or another
	// object for every raw sample would exceed this allowance by hundreds.
	if largeAllocations > smallAllocations+64 {
		t.Fatalf("raw OTLP allocation count scales with samples: %.0f -> %.0f", smallAllocations, largeAllocations)
	}
	t.Logf("%dx events: %.0f -> %.0f allocations", repetitions, smallAllocations, largeAllocations)
}
