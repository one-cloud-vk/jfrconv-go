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
func TestOTLPPreservesChunkTimesSamplesAndDefaultThreads(t *testing.T) {
	p, path := protoFixture(t)
	p.credit(path, 400, 2, 400, 1500)
	worker := p.strings.intern("worker")
	other := p.strings.intern("other")
	addSpool(t, p, otlpRecord{1000, math.MaxUint64, 1400, 0, 0}, otlpRecord{1100, uint64(path.Leaf), 3, 600, uint64(worker)}, otlpRecord{1400, math.MaxUint64, 2000, 0, 0}, otlpRecord{1500, uint64(path.Leaf), 2, 400, uint64(other)})
	fields := parseWire(t, encodedProto(t, p, writeOTLP))
	resource := parseWire(t, fields[1][0].b)
	scope := parseWire(t, resource[2][0].b)
	if len(scope[2]) != 2 {
		t.Fatal("chunk boundaries lost")
	}
	dictionary := parseWire(t, fields[2][0].b)
	table := wireStringTable(dictionary, 5)
	attributes := make(map[uint64][2]string)
	for id, raw := range dictionary[6] {
		a := parseWire(t, raw.b)
		v := parseWire(t, a[2][0].b)
		attributes[uint64(id)] = [2]string{string(a[1][0].b), string(v[1][0].b)}
	}
	for i, raw := range scope[2] {
		chunk := parseWire(t, raw.b)
		start := []uint64{1000, 1400}[i]
		duration := []uint64{400, 600}[i]
		if wireUint(t, chunk, 4) != start || wireUint(t, chunk, 5) != duration {
			t.Fatal("chunk times")
		}
		if len(chunk[1]) != 2 || len(chunk[2]) != 1 {
			t.Fatal("sample types/samples")
		}
		for n, raw := range chunk[1] {
			s := parseWire(t, raw.b)
			if table[wireUint(t, s, 1)] != "allocations" || table[wireUint(t, s, 2)] != []string{"count", "bytes"}[n] || wireUint(t, s, 3) != 2 {
				t.Fatal("sample types")
			}
		}
		sample := parseWire(t, chunk[2][0].b)
		if wireUint(t, sample, 6) != []uint64{1100, 1500}[i] || wireUint(t, sample, 1) != 0 || wireUint(t, sample, 2) != 2 {
			t.Fatal("sample time/stack slice")
		}
		if !reflect.DeepEqual(packedWire(t, sample[3][0].b), [][]uint64{{3, 600}, {2, 400}}[i]) {
			t.Fatal("sample values")
		}
		if !reflect.DeepEqual(packedWire(t, chunk[3][0].b), []uint64{2, 1}) {
			t.Fatal("location indexes")
		}
		labels := make(map[string]string)
		for _, a := range sample[4] {
			kv := attributes[a.n]
			labels[kv[0]] = kv[1]
		}
		if labels["thread.name"] != []string{"worker", "other"}[i] || labels["category"] != "[c2_comp]" {
			t.Fatal(labels)
		}
	}
	paths := append([]string(nil), p.scratch...)
	p.close()
	for _, path := range paths {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("scratch retained", path, err)
		}
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
	addSpool(t, p, otlpRecord{0, math.MaxUint64, 100, 0, 0}, otlpRecord{10, 0, 3, 3, 0})
	fields = parseWire(t, encodedProto(t, p, writeOTLP))
	resource := parseWire(t, fields[1][0].b)
	scope := parseWire(t, resource[2][0].b)
	chunk := parseWire(t, scope[2][0].b)
	sample = parseWire(t, chunk[2][0].b)
	if wireUint(t, sample, 2) != 0 || len(chunk[3]) != 1 || len(chunk[3][0].b) != 0 {
		t.Fatal("missing empty OTLP locations")
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
			err := writer(p, out, p.options)
			if err == nil {
				err = out.Flush()
			}
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatal(err)
			}
		})
	}
}
