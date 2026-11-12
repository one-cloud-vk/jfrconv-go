// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

type writerFunc func(*profile, *bufio.Writer, Options) error

type wireValue struct {
	n uint64
	b []byte
}
type wireFields map[uint64][]wireValue

func parseWire(t *testing.T, b []byte) wireFields {
	t.Helper()
	fields := make(wireFields)
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatal("invalid wire key")
		}
		b = b[n:]
		var value wireValue
		switch key & 7 {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				t.Fatal("invalid wire integer")
			}
			value.n = v
			b = b[n:]
		case 1:
			if len(b) < 8 {
				t.Fatal("truncated fixed64")
			}
			value.n = binary.LittleEndian.Uint64(b[:8])
			b = b[8:]
		case 2:
			length, n := binary.Uvarint(b)
			if n <= 0 || length > uint64(len(b)-n) {
				t.Fatal("invalid wire length")
			}
			b = b[n:]
			value.b = b[:length]
			b = b[length:]
		default:
			t.Fatalf("unexpected wire type %d", key&7)
		}
		fields[key>>3] = append(fields[key>>3], value)
	}
	return fields
}
func wireUint(t *testing.T, fields wireFields, key uint64) uint64 {
	t.Helper()
	if len(fields[key]) != 1 {
		t.Fatalf("field %d count %d", key, len(fields[key]))
	}
	return fields[key][0].n
}
func packedWire(t *testing.T, b []byte) []uint64 {
	t.Helper()
	var values []uint64
	for len(b) > 0 {
		n, length := binary.Uvarint(b)
		if length <= 0 {
			t.Fatal("invalid packed integer")
		}
		values = append(values, n)
		b = b[length:]
	}
	return values
}
func wireStringTable(fields wireFields, key uint64) []string {
	var s []string
	for _, v := range fields[key] {
		s = append(s, string(v.b))
	}
	return s
}
func protoFixture(t *testing.T) (*profile, cachedPath) {
	t.Helper()
	o := DefaultOptions()
	o.Mode = ModeAllocation
	o.Total = true
	o.TempDir = t.TempDir()
	p := newProfile(o)
	p.StartNS = 1000
	p.EndNS = 2000
	thread := p.frameName("[worker tid=9]", 3, 0, 0, 1)
	category := p.frameName("[c2_comp]", 2, 0, 0, 2)
	main := p.frameName("pkg/Main.run", 1, 17, 0, 0)
	leaf := p.frameName("leaf", 3, 0, 0, 0)
	path := p.path([]uint32{thread, category, main, leaf}, true)
	p.credit(path, 600, 3, 600, 1100)
	t.Cleanup(p.close)
	return p, path
}
func encodedProto(t *testing.T, p *profile, write writerFunc) []byte {
	t.Helper()
	var out bytes.Buffer
	buffer := bufio.NewWriterSize(&out, 256)
	if err := write(p, buffer, p.options); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Flush(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestProtobufNestedLengthsAndMaxVarint(t *testing.T) {
	var out bytes.Buffer
	buffer := bufio.NewWriter(&out)
	q := pbOut{out: buffer}
	q.h(1, pbUSize(1, 150)+pbMSize(2, 3))
	q.u(1, 150)
	q.s(2, "abc")
	buffer.Flush()
	if want := []byte{10, 8, 8, 150, 1, 18, 3, 'a', 'b', 'c'}; !bytes.Equal(out.Bytes(), want) {
		t.Fatal(out.Bytes())
	}
	out.Reset()
	q.v(math.MaxUint64)
	buffer.Flush()
	if len(out.Bytes()) != 10 || out.Bytes()[9] != 1 {
		t.Fatal(out.Bytes())
	}
}
func TestPprofResolvesFramesLabelsUnitsAndTimes(t *testing.T) {
	p, _ := protoFixture(t)
	data := encodedProto(t, p, writePprof)
	fields := parseWire(t, data)
	table := wireStringTable(fields, 6)
	types := parseWire(t, fields[1][0].b)
	if table[wireUint(t, types, 1)] != "allocations" || table[wireUint(t, types, 2)] != "bytes" {
		t.Fatal("sample type", types, table)
	}
	functions := make(map[uint64]string)
	for _, raw := range fields[5] {
		f := parseWire(t, raw.b)
		functions[wireUint(t, f, 1)] = table[wireUint(t, f, 2)]
	}
	if len(functions) != 2 {
		t.Fatal("unused or duplicate functions", functions)
	}
	locations := make(map[uint64]string)
	lines := make(map[uint64]uint64)
	for _, raw := range fields[4] {
		l := parseWire(t, raw.b)
		line := parseWire(t, l[4][0].b)
		id := wireUint(t, l, 1)
		locations[id] = functions[wireUint(t, line, 1)]
		lines[id] = wireUint(t, line, 2)
	}
	if len(locations) != 2 || len(fields[2]) != 1 {
		t.Fatal("locations/samples", len(locations), len(fields[2]))
	}
	sample := parseWire(t, fields[2][0].b)
	ids := packedWire(t, sample[1][0].b)
	if len(ids) != 2 || locations[ids[0]] != "leaf" || locations[ids[1]] != "pkg/Main.run" || lines[ids[1]] != 17 {
		t.Fatal(ids, locations, lines)
	}
	if wireUint(t, sample, 2) != 600 {
		t.Fatal("sample value")
	}
	labels := make(map[string]string)
	for _, raw := range sample[3] {
		l := parseWire(t, raw.b)
		labels[table[wireUint(t, l, 1)]] = table[wireUint(t, l, 2)]
	}
	if !reflect.DeepEqual(labels, map[string]string{"thread": "[worker tid=9]", "category": "[c2_comp]"}) {
		t.Fatal(labels)
	}
	if wireUint(t, fields, 9) != 1000 || wireUint(t, fields, 10) != 1000 {
		t.Fatal("recording time")
	}
}
func TestGzipPprofMatchesUncompressedWire(t *testing.T) {
	p, _ := protoFixture(t)
	plain := encodedProto(t, p, writePprof)
	compressed := encodedProto(t, p, writePprofGzip)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatal("gzip changed protobuf")
	}
}
func addSpool(t *testing.T, p *profile, records ...otlpRecord) {
	t.Helper()
	file, err := p.temp()
	if err != nil {
		t.Fatal(err)
	}
	p.spool = file
	for _, record := range records {
		if err = writeOTLPRecord(file, record); err != nil {
			t.Fatal(err)
		}
	}
}
func fixed64Wire(t *testing.T, b []byte) []uint64 {
	t.Helper()
	if len(b)%8 != 0 {
		t.Fatal("invalid packed fixed64")
	}
	var values []uint64
	for len(b) > 0 {
		values = append(values, binary.LittleEndian.Uint64(b[:8]))
		b = b[8:]
	}
	return values
}
func TestOTLPV45PreservesPerEventArraysPeriodDictionaryAndDefaultThreads(t *testing.T) {
	p, path := protoFixture(t)
	worker := p.strings.intern("worker")
	other := p.strings.intern("other")
	addSpool(t, p,
		otlpRecord{1000, math.MaxUint64, 1400, 20000000, 0},
		otlpRecord{1100, uint64(path.Leaf), 600, 7, uint64(worker)},
		otlpRecord{1200, uint64(path.Leaf), 1, 8, uint64(other)},
		otlpRecord{1050, uint64(path.Leaf), math.MaxUint64, 7, uint64(worker)},
		otlpRecord{1400, math.MaxUint64, 2000, 50000000, 0},
		otlpRecord{1500, uint64(path.Leaf), 1, 9, uint64(worker)},
		otlpRecord{1700, uint64(path.Leaf), 1, 9, uint64(worker)})
	budgetBefore := p.budget.owned
	fields := parseWire(t, encodedProto(t, p, writeOTLP))
	if p.budget.owned != budgetBefore {
		t.Fatal("successful OTLP export retained temporary dictionary charges")
	}
	resource := parseWire(t, fields[1][0].b)
	scope := parseWire(t, resource[2][0].b)
	if len(scope[2]) != 2 {
		t.Fatal("chunk boundaries lost")
	}
	dictionary := parseWire(t, fields[2][0].b)
	table := wireStringTable(dictionary, 5)
	if len(dictionary[1]) != 1 || len(dictionary[1][0].b) != 0 || len(dictionary[2][0].b) != 0 {
		t.Fatal("default mapping/location is not empty")
	}
	link := parseWire(t, dictionary[4][0].b)
	if !bytes.Equal(link[1][0].b, make([]byte, 16)) || !bytes.Equal(link[2][0].b, make([]byte, 8)) {
		t.Fatal("missing default zero link")
	}
	if len(dictionary[6][0].b) != 0 {
		t.Fatal("default attribute must be empty")
	}
	if len(dictionary[3]) != 3 {
		t.Fatal("unused or duplicate functions", len(dictionary[3]))
	}
	attributes := make(map[uint64][2]string)
	for id, raw := range dictionary[6][1:] {
		a := parseWire(t, raw.b)
		v := parseWire(t, a[2][0].b)
		attributes[uint64(id+1)] = [2]string{table[wireUint(t, a, 1)], string(v[1][0].b)}
	}
	if len(dictionary[7]) != 2 || len(parseWire(t, dictionary[7][0].b)[1][0].b) != 0 {
		t.Fatal("global stack table/default")
	}
	stack := parseWire(t, dictionary[7][1].b)
	var names []string
	for _, id := range packedWire(t, stack[1][0].b) {
		location := parseWire(t, dictionary[2][id].b)
		line := parseWire(t, location[3][0].b)
		fn := parseWire(t, dictionary[3][wireUint(t, line, 1)].b)
		names = append(names, table[wireUint(t, fn, 1)])
	}
	if !reflect.DeepEqual(names, []string{"leaf", "pkg/Main.run"}) {
		t.Fatal(names)
	}
	for i, raw := range scope[2] {
		chunk := parseWire(t, raw.b)
		if wireUint(t, chunk, 3) != []uint64{1000, 1400}[i] || wireUint(t, chunk, 4) != []uint64{400, 600}[i] {
			t.Fatal("chunk times")
		}
		if len(chunk[1]) != 1 {
			t.Fatal("4.5 has one sample type")
		}
		for _, tag := range []uint64{1, 5} {
			typ := parseWire(t, chunk[tag][0].b)
			if len(typ[3]) != 0 || table[wireUint(t, typ, 1)] != "allocations" || table[wireUint(t, typ, 2)] != "bytes" {
				t.Fatal("sample/period types", typ)
			}
		}
		if wireUint(t, chunk, 6) != []uint64{20000000, 50000000}[i] {
			t.Fatal("period")
		}
		if len(chunk[2]) != []int{2, 1}[i] {
			t.Fatal("TID/stack groups", len(chunk[2]))
		}
		for j, raw := range chunk[2] {
			sample := parseWire(t, raw.b)
			if wireUint(t, sample, 1) != 1 {
				t.Fatal("global stack index")
			}
			attr := attributes[wireUint(t, sample, 2)]
			if attr[0] != "thread.name" || attr[1] != [][]string{{"worker", "other"}, {"worker"}}[i][j] {
				t.Fatal(attr)
			}
			timestamps := fixed64Wire(t, sample[5][0].b)
			expected := [][][]uint64{{{1100, 1050}, {1200}}, {{1500, 1700}}}[i][j]
			if !reflect.DeepEqual(timestamps, expected) {
				t.Fatal("event order/timestamps", timestamps)
			}
			if i == 0 {
				want := [][]uint64{{600, math.MaxUint64}, {1}}[j]
				if !reflect.DeepEqual(packedWire(t, sample[4][0].b), want) {
					t.Fatal("per-event selected values")
				}
			} else if len(sample[4]) != 0 {
				t.Fatal("unit values must be omitted for the entire chunk")
			}
		}
	}
	paths := append([]string(nil), p.scratch...)
	p.close()
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("scratch retained", path, err)
		}
	}
	entries, err := os.ReadDir(p.options.TempDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("per-chunk scratch retained", entries, err)
	}
}
func TestOTLPRejectsMalformedSpools(t *testing.T) {
	for _, tc := range []struct {
		name      string
		records   []otlpRecord
		truncated bool
	}{{"before-header", []otlpRecord{{1100, 1, 1, 1, 0}}, false}, {"reversed-time", []otlpRecord{{2000, math.MaxUint64, 1000, 0, 0}}, false}, {"bad-leaf", []otlpRecord{{1000, math.MaxUint64, 2000, 0, 0}, {1100, math.MaxUint64 - 1, 1, 1, 0}}, false}, {"bad-thread", []otlpRecord{{1000, math.MaxUint64, 2000, 0, 0}, {1100, 0, 1, 1, math.MaxUint64}}, false}, {"truncated", nil, true}} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := protoFixture(t)
			addSpool(t, p, tc.records...)
			if tc.truncated {
				p.spool.Write([]byte{1})
			}
			var bytes bytes.Buffer
			out := bufio.NewWriter(&bytes)
			if err := writeOTLP(p, out, p.options); err == nil {
				t.Fatal("accepted malformed spool")
			}
		})
	}
	t.Run("inconsistent-thread-group", func(t *testing.T) {
		p, path := protoFixture(t)
		other := p.strings.intern("other")
		addSpool(t, p,
			otlpRecord{1000, math.MaxUint64, 2000, 10000, 0},
			otlpRecord{1100, uint64(path.Leaf), 1, 7, 0},
			otlpRecord{1200, uint64(path.Leaf), 1, 7, uint64(other)})
		var out bytes.Buffer
		if err := writeOTLP(p, bufio.NewWriter(&out), p.options); err == nil || !strings.Contains(err.Error(), "inconsistent OTLP") {
			t.Fatal("accepted inconsistent group", err)
		}
	})
}

func TestOTLPV45LargeSingleGroupStreamsAcrossSpillAndEncodedBuffers(t *testing.T) {
	p, path := protoFixture(t)
	p.options.Total = false
	addSpool(t, p, otlpRecord{1000, math.MaxUint64, 100000, 10000000, 0})
	const count = nativeRunItems + 4097
	w := bufio.NewWriterSize(p.spool, 64*1024)
	for i := 0; i < count; i++ {
		// Descending timestamps ensure grouping preserves recording order,
		// rather than accidentally sorting timestamps inside a sample.
		if err := writeOTLPRecord(w, otlpRecord{uint64(100000 - i), uint64(path.Leaf), 1, 42, 0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	fields := parseWire(t, encodedProto(t, p, writeOTLP))
	resource := parseWire(t, fields[1][0].b)
	scope := parseWire(t, resource[2][0].b)
	chunk := parseWire(t, scope[2][0].b)
	if len(chunk[2]) != 1 {
		t.Fatal("a large group was split", len(chunk[2]))
	}
	sample := parseWire(t, chunk[2][0].b)
	if len(sample[4]) != 0 {
		t.Fatal("unit values must be omitted")
	}
	timestamps := fixed64Wire(t, sample[5][0].b)
	if len(timestamps) != count {
		t.Fatal("events were aggregated or lost", len(timestamps))
	}
	for i, value := range timestamps {
		if value != uint64(100000-i) {
			t.Fatal("event sequence changed", i, value)
		}
	}
	dictionary := parseWire(t, fields[2][0].b)
	table := wireStringTable(dictionary, 5)
	typ := parseWire(t, chunk[1][0].b)
	if table[wireUint(t, typ, 2)] != "count" {
		t.Fatal("count selection must use a single count sample type")
	}
	p.close()
	entries, err := os.ReadDir(p.options.TempDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("external sort scratch retained", entries, err)
	}
}

func TestProtobufInternedFunctionsAndOTLPLinesIgnoreFrameKindAndBCI(t *testing.T) {
	p := newProfile(DefaultOptions())
	p.options.TempDir = t.TempDir()
	t.Cleanup(p.close)
	p.frameName("unused", 1, 123, 0, 0)
	paths := make([]cachedPath, 0, 3)
	for _, f := range []frame{
		{Name: p.strings.intern("same.name"), Kind: 1, Line: 17, BCI: 1},
		{Name: p.strings.intern("same.name"), Kind: 2, Line: 17, BCI: 9},
		{Name: p.strings.intern("same.name"), Kind: 3, Line: 18},
	} {
		path := p.path([]uint32{p.frameKey(f)}, false)
		p.credit(path, 1, 1, 1, 100)
		paths = append(paths, path)
	}
	pprof := parseWire(t, encodedProto(t, p, writePprof))
	if len(pprof[5]) != 1 {
		t.Fatal("pprof must emit one used function per interned name", len(pprof[5]))
	}
	addSpool(t, p, otlpRecord{0, math.MaxUint64, 200, 10000000, 0})
	for i, path := range paths {
		if err := writeOTLPRecord(p.spool, otlpRecord{100, uint64(path.Leaf), 1, uint64(i), 0}); err != nil {
			t.Fatal(err)
		}
	}
	fields := parseWire(t, encodedProto(t, p, writeOTLP))
	dictionary := parseWire(t, fields[2][0].b)
	if len(dictionary[3]) != 2 || len(dictionary[2]) != 3 {
		t.Fatal("OTLP requires empty defaults, one used function, two source lines", len(dictionary[3]), len(dictionary[2]))
	}
}

func TestOTLPSorterCompactsRunsAndPreservesGroupEventOrder(t *testing.T) {
	dir := t.TempDir()
	sorter := otlpSorter{nativeSorter: nativeSorter{dir: dir, runItems: 2}}
	defer sorter.close()
	const count = mergeFanIn * 5
	for i := 0; i < count; i++ {
		if err := sorter.push(nativeRecord{Time: uint64(i % 7), Address: uint64(i)}); err != nil {
			t.Fatal(err)
		}
		if len(sorter.runs) >= mergeFanIn {
			t.Fatal("run metadata grew beyond the merge bound")
		}
	}
	last := make(map[uint64]uint64)
	seen := 0
	group := uint64(0)
	if err := sorter.each(func(r nativeRecord) error {
		if r.Time < group {
			t.Fatal("group sort order", r)
		}
		if prior, ok := last[r.Time]; ok && r.Address <= prior {
			t.Fatal("original event order changed", r)
		}
		last[r.Time], group = r.Address, r.Time
		seen++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != count {
		t.Fatal("events lost across compacted runs", seen)
	}
	sorter.close()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("sort runs retained", entries, err)
	}
}

func TestOTLPSorterEqualLevelCompactionBoundsRewrittenRecords(t *testing.T) {
	sorter := otlpSorter{nativeSorter: nativeSorter{dir: t.TempDir(), runItems: 1}}
	defer sorter.close()
	const count = otlpMergeFanIn * otlpMergeFanIn * otlpMergeFanIn
	for i := 0; i < count; i++ {
		if err := sorter.push(nativeRecord{Time: uint64(i % 3), Address: uint64(i)}); err != nil {
			t.Fatal(err)
		}
		if len(sorter.runs) > mergeFanIn {
			t.Fatal("run descriptor bound exceeded")
		}
	}
	// Three equal-level merge passes for 8^3 runs. Repeated whole-prefix
	// merging rewrites more than this bound and grows quadratically.
	if sorter.mergedRecords != count*3 || len(sorter.runs) != 1 {
		t.Fatal("unexpected merge I/O amplification", sorter.mergedRecords, len(sorter.runs))
	}
	seen := 0
	if err := sorter.each(func(r nativeRecord) error { seen++; return nil }); err != nil || seen != count {
		t.Fatal("compaction lost events", seen, err)
	}
}

func TestOTLPSorterLimitFailsExplicitlyAndCleansRuns(t *testing.T) {
	dir := t.TempDir()
	sorter := otlpSorter{nativeSorter: nativeSorter{dir: dir, runItems: 1}, maxLevels: 1}
	for i := 0; i < otlpMergeFanIn-1; i++ {
		if err := sorter.push(nativeRecord{Time: uint64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sorter.push(nativeRecord{}); err == nil || !strings.Contains(err.Error(), "bounded external sort limit") {
		t.Fatal("expected an explicit bounded-sort limit error", err)
	}
	sorter.close()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed bounded sort retained scratch", entries, err)
	}
}

func TestOTLPDictionaryBudgetRejectsBeforeAllocatingAndCleansScratch(t *testing.T) {
	p, path := protoFixture(t)
	addSpool(t, p, otlpRecord{1000, math.MaxUint64, 2000, 10000, 0}, otlpRecord{1100, uint64(path.Leaf), 1, 1, 0})
	before := p.budget.owned
	p.budget.limit = before + 1
	func() {
		defer func() {
			if got := recover(); got != ErrMemoryBudget {
				t.Fatalf("expected dictionary memory budget error, got %v", got)
			}
		}()
		var out bytes.Buffer
		if err := writeOTLP(p, bufio.NewWriter(&out), p.options); err != nil {
			t.Fatal(err)
		}
	}()
	if p.budget.owned != before {
		t.Fatal("failed dictionary reservation changed budget", before, p.budget.owned)
	}
	p.close()
	entries, err := os.ReadDir(p.options.TempDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("budget failure retained scratch", entries, err)
	}
}

func TestOTLPDictionaryBudgetCountsTransientVectorAndMapGrowth(t *testing.T) {
	p, path := protoFixture(t)
	before := p.budget.owned
	d := makeOTLPDictionary(p)
	for len(d.attributes) < cap(d.attributes) || cap(d.attributes) == 1 {
		d.appendID(&d.attributes, 0)
	}
	owned := p.budget.owned
	// The final net capacity increase would fit, but old+new live buffers do
	// not. This must reject before allocating/copying a replacement vector.
	p.budget.limit = owned + uint64(cap(d.attributes))*4
	func() {
		defer func() {
			if got := recover(); got != ErrMemoryBudget {
				t.Fatalf("expected transient-vector budget error, got %v", got)
			}
		}()
		d.appendID(&d.attributes, 0)
	}()
	if p.budget.owned != owned {
		t.Fatal("failed vector reservation changed budget")
	}
	p.budget.limit = 0
	d.addEvent(p, otlpRecord{1100, uint64(path.Leaf), 1, 1, 0})
	owned = p.budget.owned
	p.budget.limit = owned + 256 + 95 // one unique map entry needs96 bytes
	func() {
		defer func() {
			if got := recover(); got != ErrMemoryBudget {
				t.Fatalf("expected line-map budget error, got %v", got)
			}
		}()
		d.finish(p)
	}()
	if p.budget.owned != owned {
		t.Fatal("temporary line-map charge was not released")
	}
	d.close()
	if p.budget.owned != before {
		t.Fatal("dictionary charges were not released", before, p.budget.owned)
	}
}
func TestEmptyStacksHavePresentEmptyLocationFields(t *testing.T) {
	p := newProfile(DefaultOptions())
	p.options.TempDir = t.TempDir()
	p.credit(cachedPath{}, 3, 3, 3, 10)
	defer p.close()
	fields := parseWire(t, encodedProto(t, p, writePprof))
	sample := parseWire(t, fields[2][0].b)
	if len(sample[1]) != 1 || len(sample[1][0].b) != 0 {
		t.Fatal("missing empty pprof locations")
	}
	addSpool(t, p, otlpRecord{0, math.MaxUint64, 100, 10000000, 0}, otlpRecord{10, 0, 3, 1, 0})
	fields = parseWire(t, encodedProto(t, p, writeOTLP))
	resource := parseWire(t, fields[1][0].b)
	scope := parseWire(t, resource[2][0].b)
	chunk := parseWire(t, scope[2][0].b)
	sample = parseWire(t, chunk[2][0].b)
	dictionary := parseWire(t, fields[2][0].b)
	stack := parseWire(t, dictionary[7][0].b)
	if wireUint(t, sample, 1) != 0 || len(stack[1]) != 1 || len(stack[1][0].b) != 0 {
		t.Fatal("missing empty OTLP stack")
	}
}

type brokenProtoWriter struct{}

func (brokenProtoWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestProtobufWritersPropagateOutputErrors(t *testing.T) {
	for name, writer := range map[string]writerFunc{"pprof": writePprof, "pb.gz": writePprofGzip, "otlp": writeOTLP} {
		t.Run(name, func(t *testing.T) {
			p, path := protoFixture(t)
			p.strings.intern(strings.Repeat("x", 65536))
			addSpool(t, p, otlpRecord{1000, math.MaxUint64, 2000, 0, 0}, otlpRecord{1100, uint64(path.Leaf), 3, 600, 0})
			out := bufio.NewWriterSize(brokenProtoWriter{}, 16)
			budgetBefore := p.budget.owned
			err := writer(p, out, p.options)
			if err == nil {
				err = out.Flush()
			}
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal(err)
			}
			if p.budget.owned != budgetBefore {
				t.Fatal("output error retained temporary dictionary charges")
			}
		})
	}
}
