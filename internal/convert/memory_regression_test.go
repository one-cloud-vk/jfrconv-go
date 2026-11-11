// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"encoding/binary"
	"io"
	"jfrconv-go/internal/jfr"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"
)

func TestPathResolverReusesScratchAndPreservesOwnedTiers(t *testing.T) {
	reader, err := jfr.Open(fixturePath("mixed.jfr"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	chunk, err := reader.NextChunk()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("compiled-path-without-allocation", func(t *testing.T) {
		o := DefaultOptions()
		p := newProfile(o)
		defer p.close()
		resolver := pathResolver{names: make(nameCache)}
		event := jfr.Event{Kind: jfr.Execution, StackID: 1}
		if _, ok := resolver.resolve(chunk, &event, o, p, "collapsed"); !ok {
			t.Fatal("fixture stack rejected")
		}
		allocations := testing.AllocsPerRun(20, func() {
			if _, ok := resolver.resolve(chunk, &event, o, p, "collapsed"); !ok {
				t.Fatal("warmed stack rejected")
			}
		})
		if allocations != 0 {
			t.Fatalf("warmed path resolution allocates %.0f objects", allocations)
		}
	})

	t.Run("tiers-and-string-ownership", func(t *testing.T) {
		o := DefaultOptions()
		o.Threads, o.Classify, o.Reverse, o.Lines = true, true, true, true
		p := newProfile(o)
		defer p.close()
		resolver := pathResolver{names: make(nameCache)}
		event := jfr.Event{Kind: jfr.Execution, StackID: 3, TID: 1}
		first, ok := resolver.resolve(chunk, &event, o, p, "collapsed")
		if !ok || len(first.Tiers) == 0 {
			t.Fatal("fixture needs interpreted/inlined tier counts")
		}
		tiers := append([]tierAt(nil), first.Tiers...)
		for _, id := range []uint64{1, 2, 3, 1} {
			event.StackID = id
			if _, ok := resolver.resolve(chunk, &event, o, p, "collapsed"); !ok {
				t.Fatal("fixture stack rejected")
			}
		}
		if !slices.Equal(first.Tiers, tiers) {
			t.Fatalf("cached tiers alias reusable scratch: %v != %v", first.Tiers, tiers)
		}
		for _, d := range resolver.descriptions[:cap(resolver.descriptions)] {
			if d.Name != "" {
				t.Fatal("description scratch retains strings")
			}
		}
		for _, pair := range resolver.classification[:cap(resolver.classification)] {
			if pair.Name != "" {
				t.Fatal("classification scratch retains strings")
			}
		}
	})

	t.Run("filter-before-interning", func(t *testing.T) {
		o := DefaultOptions()
		o.Include = regexp.MustCompile("never-matches")
		p := newProfile(o)
		defer p.close()
		resolver := pathResolver{names: make(nameCache)}
		event := jfr.Event{Kind: jfr.Execution, StackID: 3}
		if _, ok := resolver.resolve(chunk, &event, o, p, "collapsed"); ok {
			t.Fatal("nonmatching path accepted")
		}
		if p.frames.n != 1 || p.nodes.n != 1 || p.strings.refs.n != 1 {
			t.Fatal("rejected path grew the global profile")
		}
	})
}

// Repeat the event area of an actual committed fixture while keeping exactly
// one chunk and the same metadata/constant pool. Thus event volume changes but
// dictionary size, distinct stacks, output size and chunk count do not.
func repeatedEventFixture(t *testing.T, name string, repeats int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 68 || string(data[:4]) != "FLR\x00" {
		t.Fatal("invalid seed fixture")
	}
	size := binary.BigEndian.Uint64(data[8:16])
	cp := binary.BigEndian.Uint64(data[16:24])
	meta := binary.BigEndian.Uint64(data[24:32])
	if repeats < 1 || meta < 68 || cp < meta || size > uint64(len(data)) {
		t.Fatal("unsupported seed fixture layout")
	}
	events := data[68:meta]
	tail := data[meta:size]
	delta := uint64(len(events)) * uint64(repeats-1)
	header := append([]byte(nil), data[:68]...)
	binary.BigEndian.PutUint64(header[8:16], size+delta)
	binary.BigEndian.PutUint64(header[16:24], cp+delta)
	binary.BigEndian.PutUint64(header[24:32], meta+delta)
	path := filepath.Join(t.TempDir(), "repeated.jfr")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	writer := bufio.NewWriterSize(file, 128*1024)
	if _, err = writer.Write(header); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < repeats; n++ {
		if _, err = writer.Write(events); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = writer.Write(tail); err != nil {
		t.Fatal(err)
	}
	if err = writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCPUConversionAllocationCountDoesNotScaleWithSampleCount(t *testing.T) {
	const repetitions = 100000
	small := repeatedEventFixture(t, "mixed.jfr", 1)
	large := repeatedEventFixture(t, "mixed.jfr", repetitions)
	o := DefaultOptions()
	o.CPU = true
	measure := func(path string) (float64, uint64, Stats) {
		var result Stats
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		allocations := testing.AllocsPerRun(3, func() {
			var err error
			result, err = Run(path, io.Discard, "collapsed", o)
			if err != nil {
				t.Fatal(err)
			}
		})
		runtime.ReadMemStats(&after)
		// AllocsPerRun performs one warmup plus the three measured calls.
		return allocations, (after.TotalAlloc - before.TotalAlloc) / 4, result
	}
	smallAlloc, smallBytes, smallStats := measure(small)
	largeAlloc, largeBytes, largeStats := measure(large)
	if smallStats.Units == 0 || largeStats.Units != smallStats.Units*repetitions || largeStats.Samples != smallStats.Samples*repetitions {
		t.Fatalf("conversion did not process the expanded data: small%+v large%+v", smallStats, largeStats)
	}
	// Small map/GC bookkeeping variation is allowed. A per-event allocation,
	// retained event slice or repeated path construction would exceed this by
	// orders of magnitude for the 100000-fold event expansion.
	if largeAlloc > smallAlloc+128 {
		t.Fatalf("allocation count grows with sample volume: %.0f -> %.0f for %d-fold events", smallAlloc, largeAlloc, repetitions)
	}
	if largeBytes > smallBytes+128*1024 {
		t.Fatalf("allocated bytes grow with sample volume: %d -> %d for %d-fold events", smallBytes, largeBytes, repetitions)
	}
	t.Logf("event expansion %dx; conversion allocations %.0f -> %.0f; allocated bytes/call %d -> %d; units %d -> %d", repetitions, smallAlloc, largeAlloc, smallBytes, largeBytes, smallStats.Units, largeStats.Units)
}

func TestMergedSpanFiltersReleaseOversizedBackingArrays(t *testing.T) {
	path := repeatedEventFixture(t, "cross-span.jfr", 4096)
	filter, err := readSpans(path, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(filter) != 1 {
		t.Fatalf("unexpected span threads: %d", len(filter))
	}
	for _, pairs := range filter {
		if len(pairs) != 1 || cap(pairs) != 1 {
			t.Fatalf("merged interval retains original event-sized capacity: len%d cap%d", len(pairs), cap(pairs))
		}
	}
}
