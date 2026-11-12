// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package jfr

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUTF8MatchesRecordedJDK21ReplacementGoldens(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "java-utf8-goldens.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Cases []struct {
			Name     string `json:"case"`
			Input    string `json:"input_hex"`
			Expected string `json:"expected_utf8_hex"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &golden); err != nil || len(golden.Cases) != 16 {
		t.Fatal("invalid Java byte goldens", err)
	}
	for _, tc := range golden.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			input, err := hex.DecodeString(tc.Input)
			if err != nil {
				t.Fatal(err)
			}
			payload := append([]byte{3}, varBytes(uint64(len(input)))...)
			payload = append(payload, input...)
			in := testInput(t, payload)
			var arena byteArena
			ref, present := in.refString(nil, &arena)
			if in.err != nil || !present || hex.EncodeToString(arena.bytes(ref)) != tc.Expected {
				t.Fatalf("UTF8 got %x want %s present=%v err=%v", arena.bytes(ref), tc.Expected, present, in.err)
			}
		})
	}
}

func TestUTF8ExpansionChecksArenaBudgetBeforeAllocatingDecodedText(t *testing.T) {
	const size = 8192
	data := append([]byte{3}, varBytes(size)...)
	data = append(data, bytes.Repeat([]byte{0x80}, size)...)
	seed := testInput(t, data)
	seed.refill()
	allocations := testing.AllocsPerRun(10, func() {
		c := Chunk{budget: size}
		arena := byteArena{owner: &c, exact: true}
		in := seed
		_, present := in.refString(nil, &arena)
		if !errors.Is(in.err, ErrBudget) || present || len(arena.blocks) != 1 || len(arena.blocks[0]) != size || c.reserved != size {
			t.Fatalf("normalization bypassed budget: present=%v blocks=%d reserved=%d err=%v", present, len(arena.blocks), c.reserved, in.err)
		}
	})
	if allocations > 4 {
		t.Fatalf("budget denial allocated temporary decoded strings/buffers: %.0f allocations", allocations)
	}
	t.Logf("denied UTF8 expansion: %.0f allocations for raw arena/owner only", allocations)
}

func testInput(t *testing.T, data []byte) input {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return newInput(file, uint64(len(data)))
}
func varBytes(value uint64) []byte {
	var out []byte
	for i := 0; i < 8; i++ {
		if value < 128 {
			return append(out, byte(value))
		}
		out = append(out, byte(value&127)|128)
		value >>= 7
	}
	return append(out, byte(value))
}
func fixture(t *testing.T, name string) *Reader {
	t.Helper()
	r, err := Open(filepath.Join("..", "..", "tests", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func TestVarlongFullWidthAndSignedDeltaBits(t *testing.T) {
	for _, value := range []uint64{0, 1, 127, 128, 16383, 16384, 1 << 55, 1 << 56, math.MaxUint64 - 1024, math.MaxUint64} {
		in := testInput(t, varBytes(value))
		if got := in.u(); got != value || in.err != nil {
			t.Fatalf("%d: got %d err %v", value, got, in.err)
		}
	}
	in := testInput(t, bytes.Repeat([]byte{255}, 9))
	if got := in.u(); got != math.MaxUint64 {
		t.Fatal(got)
	}
}
func TestVarlongAcrossBufferAndBackwardSeek(t *testing.T) {
	data := make([]byte, bufferSize-4)
	data = append(data, varBytes(math.MaxUint64)...)
	data = append(data, 66)
	in := testInput(t, data)
	prefix := make([]byte, bufferSize-4)
	in.take(prefix)
	if in.u() != math.MaxUint64 || in.b() != 66 || in.err != nil {
		t.Fatal(in.err)
	}
	in.seek(bufferSize - 4)
	if in.u() != math.MaxUint64 {
		t.Fatal("backward seek")
	}
	in.seek(0)
	if in.b() != 0 {
		t.Fatal("seek zero")
	}
}
func TestBoundariesFailBeforeFastCallback(t *testing.T) {
	in := testInput(t, []byte{128})
	in.u()
	if in.err == nil {
		t.Fatal("accepted truncated varint")
	}
	in = testInput(t, []byte{2, 42, 99, 99})
	kind, end := in.record(0, 4)
	if kind != 42 || end != 2 {
		t.Fatal(kind, end)
	}
	in.b()
	if in.err == nil {
		t.Fatal("crossed record")
	}
	in = testInput(t, []byte{5, 42, 0})
	in.record(0, 3)
	if in.err == nil {
		t.Fatal("accepted oversized record")
	}
	data := make([]byte, 100)
	copy(data, []byte{1, 2, 3, 4})
	r := Reader{in: testInput(t, data)}
	r.in.refill()
	r.execution(false, false, 3)
	if r.in.err == nil || r.in.position() != 0 {
		t.Fatal("fast execution crossed boundary")
	}
	data[0] = 128
	in = testInput(t, data)
	in.refill()
	in.limit = 1
	in.u()
	if in.err == nil || in.position() != 0 {
		t.Fatal("fast varint crossed boundary")
	}
}
func TestStringsAllEncodingsAndCPReferences(t *testing.T) {
	for _, tc := range []struct {
		data  []byte
		want  string
		valid bool
	}{{[]byte{0}, "", false}, {[]byte{1}, "", true}, {[]byte{3, 2, 0xce, 0xb1}, "α", true}, {[]byte{5, 1, 0xe9}, "é", true}, {append(append([]byte{4, 2}, varBytes(0xd83d)...), varBytes(0xde00)...), "😀", true}} {
		in := testInput(t, tc.data)
		var arena byteArena
		ref, valid := in.refString(nil, &arena)
		if valid != tc.valid || arena.string(ref) != tc.want || in.err != nil {
			t.Fatalf("%v => %q %v %v", tc.data, arena.string(ref), valid, in.err)
		}
	}
	for _, bad := range [][]byte{{3, 127}, {255}, {2, 12}, {4, 1, 0x80, 0x80, 4}} {
		in := testInput(t, bad)
		var arena byteArena
		in.refString(nil, &arena)
		if in.err == nil {
			t.Fatalf("accepted bad string %v", bad)
		}
	}
	c := Chunk{strings: make(map[uint64]strRef)}
	ref, _ := c.text.store("constant-α")
	c.strings[42] = ref
	in := testInput(t, []byte{2, 42})
	if in.valueString(&c) != "constant-α" || in.err != nil {
		t.Fatal(in.err)
	}
}
func TestMixedFixtureAllFamiliesAndChunkOwnership(t *testing.T) {
	r := fixture(t, "mixed.jfr")
	r.TrackStackOrder = true
	counts := make(map[EventKind]int)
	values := make(map[EventKind]uint64)
	var callbackAddress *Event
	chunks := 0
	var first *Chunk
	for {
		c, err := r.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		chunks++
		if !reflect.DeepEqual(c.StackOrder, []uint64{3, 1, 2}) {
			t.Fatalf("Java dictionary traversal %v", c.StackOrder)
		}
		if c.ThreadName(1) != "worker-α" {
			t.Fatal(c.ThreadName(1))
		}
		if c.Symbol(7) != "[B" {
			t.Fatal(c.Symbol(7))
		}
		method, ok := c.Method(2)
		if !ok || method.Class != 2 || method.Name != 6 {
			t.Fatal(method, ok)
		}
		class, ok := c.Class(1)
		if !ok || class.Name != 2 {
			t.Fatal(class, ok)
		}
		stack := c.Stack(1)
		if stack.Len() != 2 || stack.At(0) != (StackFrame{Method: 2, Kind: 1, Line: 20, BCI: 3}) {
			t.Fatal(stack)
		}
		if first == nil {
			first = c
		} else {
			if first.Symbol(2) != "demo/Main" || c.Symbol(2) != "second/Main" {
				t.Fatal("CP identity escaped chunk")
			}
		}
		err = r.Events(c, MaskAll, func(e *Event) error {
			if callbackAddress == nil {
				callbackAddress = e
			} else if callbackAddress != e {
				t.Fatal("event not reused")
			}
			counts[e.Kind]++
			values[e.Kind] += e.Value
			if e.Kind == Span && e.Tag != "request-α" && e.Tag != "background" {
				t.Fatal(e.Tag)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if chunks != 2 {
		t.Fatal(chunks)
	}
	if want := map[EventKind]int{Execution: 10, Allocation: 6, Live: 2, Lock: 4, Malloc: 4, Free: 2, Span: 4}; !reflect.DeepEqual(counts, want) {
		t.Fatal(counts)
	}
	if want := map[EventKind]uint64{Execution: 26, Allocation: 2816, Live: 1024, Lock: 100000, Malloc: 12288, Free: 0, Span: 1000}; !reflect.DeepEqual(values, want) {
		t.Fatal(values)
	}
}
func TestCurrentSpanAndMethodTraceFixtures(t *testing.T) {
	r := fixture(t, "cross-span.jfr")
	spans := 0
	for {
		c, err := r.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = r.Events(c, MaskSpan, func(e *Event) error {
			spans++
			if e.Tag != "cross-request" || e.TID != 1 || e.StackID != 0 || e.Duration != 200000000 {
				t.Fatal(*e)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if spans != 1 {
		t.Fatal(spans)
	}
	r = fixture(t, "method-trace.jfr")
	c, err := r.NextChunk()
	if err != nil {
		t.Fatal(err)
	}
	if c.TicksPerSec != 2000000000 {
		t.Fatal(c.TicksPerSec)
	}
	var events []Event
	if err = r.Events(c, MaskSpan, func(e *Event) error { events = append(events, *e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].MethodID != 2 || events[0].Duration != 75000000 || events[1].StackID != 0 || events[1].MethodID != 2 || events[1].Duration != 25000000 {
		t.Fatal(events)
	}
}

func TestSpanDistinguishesNullAndPresentEmptyTags(t *testing.T) {
	c := Chunk{types: map[uint64]typ{2: {name: "java.lang.String"}}, strings: map[uint64]strRef{42: {}}}
	for _, tc := range []struct {
		data        []byte
		cp, present bool
	}{{[]byte{0}, true, false}, {[]byte{42}, true, true}, {[]byte{0}, false, false}, {[]byte{1}, false, true}, {[]byte{3, 0}, false, true}, {[]byte{2, 0}, false, false}, {[]byte{2, 42}, false, true}} {
		r := Reader{in: testInput(t, tc.data), event: Event{Kind: Span}}
		r.decode(&c, typ{fields: []field{{name: "tag", typeID: 2, constantPool: tc.cp}}}, encSpan)
		if r.in.err != nil || r.event.Tag != "" || r.event.TagPresent != tc.present {
			t.Fatalf("tag %v cp=%v => present=%v, err=%v", tc.data, tc.cp, r.event.TagPresent, r.in.err)
		}
	}
}

func TestJava45CPUTimeAndNativeLockWireLayouts(t *testing.T) {
	values := func(fields ...uint64) []byte {
		var b []byte
		for _, value := range fields {
			b = append(b, varBytes(value)...)
		}
		return b
	}
	cpu := append(values(123, 456, 789), 1)
	cpu = append(cpu, values(1<<45)...)
	cpu = append(cpu, 0)
	r := Reader{in: testInput(t, cpu), event: Event{Kind: Execution, Samples: 1, Value: 1}}
	r.cpuTime()
	if r.in.err != nil || r.in.position() != uint64(len(cpu)) || r.event.Time != 123 || r.event.StackID != 456 || r.event.TID != 789 || r.event.State != CPUTimeSampleState || r.event.Samples != 1 || r.event.Value != 1 {
		t.Fatalf("CPUTimeSample: %+v err=%v position=%d", r.event, r.in.err, r.in.position())
	}
	lock := values(123, 987, 789, 456, 1<<40)
	r = Reader{in: testInput(t, lock), event: Event{Kind: NativeLock, Samples: 1, Value: 1}}
	r.decode(&Chunk{}, typ{}, encNativeLock)
	if r.in.err != nil || r.in.position() != uint64(len(lock)) || r.event.Time != 123 || r.event.Duration != 987 || r.event.Value != 987 || r.event.StackID != 456 || r.event.TID != 789 || r.event.Address != 1<<40 || r.event.ClassID != 0 {
		t.Fatalf("NativeLock: %+v err=%v", r.event, r.in.err)
	}
	if encodingFor("profiler.MethodTrace") != encUnknown || encodingFor("jdk.MethodTrace") != encMethodTrace || MaskSpan&MaskTrace == 0 {
		t.Fatal("legacy trace alias or interval/trace masks do not match 4.5")
	}
}

func TestJava45WallTimeSpanConsumedAndValidated(t *testing.T) {
	var data []byte
	for _, value := range []uint64{123, 7, 9, 0, 17, 1 << 40} {
		data = append(data, varBytes(value)...)
	}
	for _, fast := range []bool{false, true} {
		payload := append([]byte(nil), data...)
		if fast {
			payload = append(payload, make([]byte, 64)...)
		}
		r := Reader{in: testInput(t, payload), event: Event{Kind: Execution, Samples: 1, Value: 1}}
		if fast {
			r.in.refill()
		}
		r.execution(true, true, uint64(len(data)))
		if r.in.err != nil || r.in.position() != uint64(len(data)) || r.event.Samples != 17 || r.event.Value != 17 {
			t.Fatalf("wall fast=%v event=%+v position=%d err=%v", fast, r.event, r.in.position(), r.in.err)
		}
	}
	r := Reader{in: testInput(t, data[:len(data)-1]), event: Event{Kind: Execution}}
	r.execution(true, true, uint64(len(data)-1))
	if r.in.err == nil {
		t.Fatal("truncated optional wall timeSpan accepted")
	}
}

func TestJava45PeriodSettingsRemainBoundedAndPersist(t *testing.T) {
	var r Reader
	setting := func(c *Chunk, name, value string) {
		var b []byte
		for _, text := range []string{name, value} {
			b = append(b, 3)
			b = append(b, varBytes(uint64(len(text)))...)
			b = append(b, text...)
		}
		r.in = testInput(t, b)
		r.activeSetting(c, typ{fields: []field{{name: "name"}, {name: "value"}}})
		if r.in.err != nil {
			t.Fatal(r.in.err)
		}
	}
	first := Chunk{}
	setting(&first, "interval", "12345")
	setting(&first, "unneeded", "not retained")
	second := Chunk{Settings: r.settings, reserved: r.settingsBytes}
	setting(&second, "wall", "6789")
	if len(r.settings) != 2 || first.Settings["wall"] != "6789" || second.Settings["interval"] != "12345" || r.settingsBytes != 9 {
		t.Fatalf("settings not cumulative/bounded: %v bytes=%d", r.settings, r.settingsBytes)
	}
	setting(&second, "interval", "1")
	if r.settingsBytes != 5 || second.Settings["interval"] != "1" {
		t.Fatal("replacement retained old settings bytes")
	}
}

func TestJava45PeriodSettingReplacementDoesNotAccumulateBudget(t *testing.T) {
	payload := []byte{3, 8, 'i', 'n', 't', 'e', 'r', 'v', 'a', 'l', 3, 6, '1', '2', '3', '4', '5', '6'}
	seed := testInput(t, payload)
	seed.refill()
	r := Reader{}
	c := Chunk{budget: 12}
	for i := 0; i < 100; i++ {
		r.in = seed
		r.activeSetting(&c, typ{fields: []field{{name: "name"}, {name: "value"}}})
		if r.in.err != nil || c.reserved != 6 || r.settingsBytes != 6 || c.Settings["interval"] != "123456" {
			t.Fatalf("setting %d accumulated previous values: reserved=%d live=%d err=%v", i, c.reserved, r.settingsBytes, r.in.err)
		}
	}
}
func TestSelectedExecutionHasNoPerEventAllocations(t *testing.T) {
	r := fixture(t, "mixed.jfr")
	c, err := r.NextChunk()
	if err != nil {
		t.Fatal(err)
	}
	var weight uint64
	visit := func(e *Event) error { weight += e.Samples; return nil }
	allocs := testing.AllocsPerRun(100, func() {
		if err := r.Events(c, MaskExecution, visit); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("execution streaming allocated %.1f objects/run", allocs)
	}
	if weight == 0 {
		t.Fatal("empty stream")
	}
}
func TestMaskSkipsUnselectedPayloadButValidatesRecords(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "mixed.jfr"))
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), original...)
	at := bytes.Index(data, []byte("request-α"))
	if at < 2 {
		t.Fatal("tag not found")
	}
	data[at-2] = 255
	path := filepath.Join(t.TempDir(), "bad-tag.jfr")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c, err := r.NextChunk()
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Events(c, MaskExecution, nil); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	if err = r.Events(c, MaskSpan, func(e *Event) error { callbacks++; return nil }); err == nil || callbacks != 0 {
		t.Fatal(err, callbacks)
	}
	data = append([]byte(nil), original...)
	data[68] = 2
	path = filepath.Join(t.TempDir(), "bad-size.jfr")
	os.WriteFile(path, data, 0600)
	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	c, err = r2.NextChunk()
	if err != nil {
		t.Fatal(err)
	}
	if err = r2.Events(c, MaskExecution, func(e *Event) error { t.Fatal("callback before boundary validation"); return nil }); err == nil {
		t.Fatal("accepted short execution record")
	}
}
func TestBudgetAndMalformedHeaders(t *testing.T) {
	r := fixture(t, "mixed.jfr")
	r.Budget = 64
	if _, err := r.NextChunk(); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "mixed.jfr"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 1, 4, 67, 68, 100, len(original) - 1} {
		path := filepath.Join(t.TempDir(), "truncated.jfr")
		os.WriteFile(path, original[:n], 0600)
		if parsed, err := Open(path); err == nil {
			parsed.Close()
			t.Fatalf("accepted %d-byte prefix", n)
		}
	}
	for _, offset := range []int{0, 4, 8, 16, 24, 56} {
		data := append([]byte(nil), original...)
		for i := offset; i < offset+4; i++ {
			data[i] = 255
		}
		if offset == 56 {
			clear(data[56:64])
		}
		path := filepath.Join(t.TempDir(), "bad-header.jfr")
		os.WriteFile(path, data, 0600)
		if parsed, err := Open(path); err == nil {
			parsed.Close()
			t.Fatalf("accepted corrupted header field %d", offset)
		}
	}
}
func TestDuplicateConstantTextDoesNotGrowArena(t *testing.T) {
	data := []byte{2, 1, 3, 3, 'o', 'n', 'e', 1, 3, 7, 'r', 'e', 'p', 'l', 'a', 'c', 'e'}
	r := Reader{in: testInput(t, data)}
	c := Chunk{symbols: make(map[uint64]strRef), strings: make(map[uint64]strRef)}
	c.text.owner = &c
	r.pool(&c, typ{name: "jdk.types.Symbol"})
	if r.in.err != nil {
		t.Fatal(r.in.err)
	}
	if c.Symbol(1) != "one" || len(c.text.blocks) != 1 || len(c.text.blocks[0]) != 3 {
		t.Fatal("duplicate CP text retained", c.Symbol(1), len(c.text.blocks[0]))
	}
}
func TestJavaDictionaryPreallocateAndClearRetainCapacity(t *testing.T) {
	c := Chunk{}
	var order javaOrder
	if err := order.preallocate(65, &c); err != nil {
		t.Fatal(err)
	}
	if len(order.keys) != 256 {
		t.Fatal(len(order.keys))
	}
	for _, key := range []uint64{5, 17, 99, 1000} {
		if err := order.put(key, &c); err != nil {
			t.Fatal(err)
		}
	}
	order.clear()
	if order.size != 0 || len(order.keys) != 256 {
		t.Fatal("clear lost Java capacity")
	}
	if err := order.preallocate(3, &c); err != nil {
		t.Fatal(err)
	}
	if len(order.keys) != 256 {
		t.Fatal("preallocate shrank Java dictionary")
	}
	if err := order.put(0, &c); err != nil {
		t.Fatalf("Java 4.5 permits raw zero: %v", err)
	}
	if err := order.put(javaUsedBit, &c); err == nil {
		t.Fatal("reserved high-bit key accepted")
	}
}

func TestJava45DictionaryRemapsBeforeHashAndVisitsRawKeys(t *testing.T) {
	var order javaOrder
	c := Chunk{}
	keys := []uint64{0, 3, 17, javaUsedBit | 7, ^uint64(0), 1, 2}
	if err := order.preallocate(len(keys), &c); err != nil {
		t.Fatal(err)
	}
	want := make([]uint64, len(order.keys))
	for _, raw := range keys {
		if err := order.put(raw, &c); err != nil {
			t.Fatal(err)
		}
		mapped := raw ^ (uint64(1) << 63)
		hash := mapped * 0xc6a4a7935bd1e995
		at := int(uint32(hash^(hash>>32))) & (len(want) - 1)
		for want[at] != 0 {
			at = (at + 1) & (len(want) - 1)
		}
		want[at] = mapped
	}
	if !reflect.DeepEqual(order.keys, want) || order.size != len(keys) {
		t.Fatalf("mapped dictionary = %v size %d, want %v", order.keys, order.size, want)
	}
	if err := order.put(0, &c); err != nil || order.size != len(keys) {
		t.Fatal("zero-key replacement changed size", err)
	}
	var visited []uint64
	for _, stored := range order.keys {
		if stored != 0 {
			visited = append(visited, stored^javaUsedBit)
		}
	}
	for _, key := range keys {
		found := false
		for _, actual := range visited {
			found = found || actual == key
		}
		if !found {
			t.Fatalf("raw key %x absent from traversal %v", key, visited)
		}
	}
}

func TestHasStackDistinguishesEmptyFromMissing(t *testing.T) {
	c := Chunk{stacks: map[uint64]stackRef{1: {}}}
	if c.Stack(1).Len() != 0 || !c.HasStack(1) || c.HasStack(2) {
		t.Fatal("empty stack identity lost")
	}
}

func TestImpossibleCountsFailBeforeArenaOrReferenceOrderAllocation(t *testing.T) {
	for _, tag := range []byte{3, 4, 5} {
		in := testInput(t, append([]byte{tag}, varBytes(16000000)...))
		var arena byteArena
		in.refString(nil, &arena)
		if in.err == nil || len(arena.blocks) != 0 {
			t.Fatalf("string tag %d allocated for impossible count", tag)
		}
	}
	for _, data := range [][]byte{varBytes(16000000), {1, 1}, append(varBytes(100000), make([]byte, 100000)...)} {
		r := Reader{in: testInput(t, data), TrackStackOrder: true}
		c := Chunk{stacks: make(map[uint64]stackRef)}
		r.pool(&c, typ{name: "jdk.types.StackTrace"})
		if r.in.err == nil || len(r.order.keys) != 0 || len(c.frames) != 0 || c.reserved != 0 {
			t.Fatalf("impossible stack count allocated: table=%d frames=%d reserved=%d error=%v", len(r.order.keys), len(c.frames), c.reserved, r.in.err)
		}
	}
}

func TestOptionalRealRecordings(t *testing.T) {
	paths := os.Getenv("JFR_REAL_INPUTS")
	if paths == "" {
		t.Skip("set JFR_REAL_INPUTS for recorded JDK/AP integration inputs")
	}
	for _, path := range filepath.SplitList(paths) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			r, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			r.TrackStackOrder = true
			chunks, events := 0, 0
			for {
				c, err := r.NextChunk()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				chunks++
				if err = r.Events(c, MaskAll, func(e *Event) error { events++; return nil }); err != nil {
					t.Fatal(err)
				}
			}
			if chunks == 0 || events == 0 {
				t.Fatal("recording contained no supported events", chunks, events)
			}
			t.Logf("%d chunks, %d recognized records", chunks, events)
		})
	}
}
