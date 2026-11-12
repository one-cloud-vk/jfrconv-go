// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package jfr

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
)

type encoding uint8

const (
	encUnknown encoding = iota
	encExecution
	encWall
	encAllocation
	encTLAB
	encLive
	encLock
	encPark
	encMalloc
	encFree
	encSpan
	encMethodTrace
	encCPUTime
	encNativeLock
	encSetting
)

func encodingFor(name string) encoding {
	switch name {
	case "jdk.ExecutionSample", "jdk.NativeMethodSample":
		return encExecution
	case "jdk.CPUTimeSample":
		return encCPUTime
	case "profiler.WallClockSample":
		return encWall
	case "jdk.ObjectAllocationInNewTLAB":
		return encTLAB
	case "jdk.ObjectAllocationOutsideTLAB", "jdk.ObjectAllocationSample":
		return encAllocation
	case "profiler.LiveObject":
		return encLive
	case "jdk.JavaMonitorEnter":
		return encLock
	case "jdk.ThreadPark":
		return encPark
	case "profiler.Malloc":
		return encMalloc
	case "profiler.Free":
		return encFree
	case "profiler.Span":
		return encSpan
	case "jdk.MethodTrace":
		return encMethodTrace
	case "profiler.NativeLock":
		return encNativeLock
	case "jdk.ActiveSetting":
		return encSetting
	default:
		return encUnknown
	}
}
func (e encoding) kind() (EventKind, bool) {
	switch e {
	case encExecution, encWall, encCPUTime:
		return Execution, true
	case encAllocation, encTLAB:
		return Allocation, true
	case encLive:
		return Live, true
	case encLock, encPark:
		return Lock, true
	case encMalloc:
		return Malloc, true
	case encFree:
		return Free, true
	case encSpan:
		return Span, true
	case encMethodTrace:
		return MethodTrace, true
	case encNativeLock:
		return NativeLock, true
	default:
		return 0, false
	}
}

type Reader struct {
	Budget               uint64
	TrackStackOrder      bool
	StartNS, EndNS       uint64
	in                   input
	fileSize, nextOffset uint64
	order                javaOrder
	event                Event
	closed               bool
	settings             map[string]string
	settingsBytes        uint64
}
type header struct{ size, cp, metadata, start, end, ticks, frequency uint64 }

func Open(path string) (*Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.Size() < int64(headerSize) {
		file.Close()
		return nil, invalid("incomplete file")
	}
	r := &Reader{in: newInput(file, uint64(info.Size())), fileSize: uint64(info.Size()), StartNS: ^uint64(0)}
	for offset := uint64(0); offset < r.fileSize; {
		h, err := r.header(offset)
		if err != nil {
			file.Close()
			return nil, err
		}
		if h.start < r.StartNS {
			r.StartNS = h.start
		}
		if h.end > r.EndNS {
			r.EndNS = h.end
		}
		offset += h.size
	}
	return r, nil
}
func (r *Reader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.in.buffer = nil
	return r.in.file.Close()
}
func (r *Reader) header(offset uint64) (header, error) {
	in := &r.in
	in.limit = r.fileSize
	in.seek(offset)
	var b [68]byte
	in.take(b[:])
	if in.err != nil {
		return header{}, in.err
	}
	if string(b[:4]) != "FLR\x00" {
		return header{}, invalid(fmt.Sprintf("chunk signature at byte %d", offset))
	}
	version := binary.BigEndian.Uint32(b[4:8])
	if version < 0x20000 || version > 0x2ffff {
		return header{}, invalid(fmt.Sprintf("unsupported version %d.%d", version>>16, version&65535))
	}
	get := func(i int) uint64 { return binary.BigEndian.Uint64(b[i : i+8]) }
	h := header{size: get(8), cp: get(16), metadata: get(24), start: get(32), ticks: get(48), frequency: get(56)}
	if h.size < headerSize || h.size > r.fileSize-offset {
		return header{}, invalid("incomplete or invalid chunk")
	}
	if h.cp < headerSize || h.cp >= h.size || h.metadata < headerSize || h.metadata >= h.size {
		return header{}, invalid("invalid checkpoint/metadata offset")
	}
	duration := get(40)
	if duration > ^uint64(0)-h.start {
		return header{}, invalid("timestamp overflow")
	}
	h.end = h.start + duration
	if h.frequency == 0 {
		return header{}, invalid("zero tick frequency")
	}
	return h, nil
}
func (r *Reader) NextChunk() (*Chunk, error) {
	if r.closed {
		return nil, os.ErrClosed
	}
	if r.in.err != nil {
		return nil, r.in.err
	}
	if r.nextOffset == r.fileSize {
		return nil, io.EOF
	}
	offset := r.nextOffset
	h, err := r.header(offset)
	if err != nil {
		return nil, err
	}
	c := &Chunk{StartNS: h.start, EndNS: h.end, StartTicks: h.ticks, TicksPerSec: h.frequency, Enums: make(map[string]map[uint64]string), threads: make(map[uint64]strRef), classes: make(map[uint64]ClassRef), symbols: make(map[uint64]strRef), methods: make(map[uint64]MethodRef), stacks: make(map[uint64]stackRef), strings: make(map[uint64]strRef), types: make(map[uint64]typ), budget: r.Budget, offset: offset, end: offset + h.size}
	c.text.owner = c
	c.Settings = r.settings
	if err := c.reserve(r.settingsBytes); err != nil {
		return nil, err
	}
	if r.TrackStackOrder {
		r.order.clear()
		if err := c.reserve(uint64(len(r.order.keys)) * 8); err != nil {
			return nil, err
		}
	} else {
		r.order = javaOrder{}
	}
	r.metadata(c, offset+h.metadata)
	if r.in.err != nil {
		return nil, r.in.err
	}
	maxID := uint64(0)
	for id := range c.types {
		if id > maxID {
			maxID = id
		}
	}
	if maxID > 65535 {
		maxID = 65535
	}
	c.encodings = make([]encoding, int(maxID)+1)
	for id, t := range c.types {
		if t.name == "profiler.WallClockSample" {
			for _, f := range t.fields {
				if f.name == "timeSpan" {
					c.wallTimeSpan = true
				}
			}
		}
		if id < uint64(len(c.encodings)) {
			c.encodings[id] = encodingFor(t.name)
		}
	}
	r.constants(c, offset+h.cp)
	if r.in.err != nil {
		return nil, r.in.err
	}
	if r.TrackStackOrder {
		if err := c.reserve(uint64(r.order.size) * 8); err != nil {
			return nil, err
		}
		c.StackOrder = make([]uint64, 0, r.order.size)
		for _, id := range r.order.keys {
			if id != 0 {
				c.StackOrder = append(c.StackOrder, id^javaUsedBit)
			}
		}
	}
	r.nextOffset = c.end
	return c, nil
}
func (r *Reader) metadata(c *Chunk, offset uint64) {
	in := &r.in
	kind, _ := in.record(offset, c.end)
	if kind != 0 {
		in.fail(invalid("metadata event type"))
		return
	}
	in.u()
	in.u()
	in.u()
	count := in.count()
	if count > 262144 {
		in.fail(invalid("metadata string table exceeds 262144 entries"))
		return
	}
	var arena byteArena
	arena.owner = c
	strings := make([]string, 0, min(count, 4096))
	for i := 0; i < count && in.err == nil; i++ {
		ref, _ := in.refString(nil, &arena)
		strings = append(strings, arena.string(ref))
	}
	if in.err == nil {
		r.element(c, strings, 0)
	}
}
func (r *Reader) element(c *Chunk, strings []string, depth int) *field {
	in := &r.in
	if in.err != nil {
		return nil
	}
	if depth >= 128 {
		in.fail(invalid("metadata nesting too deep"))
		return nil
	}
	text := func(index uint64) string {
		if index >= uint64(len(strings)) {
			in.fail(invalid("metadata string index out of range"))
			return ""
		}
		return strings[index]
	}
	name := text(in.u())
	count := in.count()
	attrs := make(map[string]string)
	for i := 0; i < count && in.err == nil; i++ {
		key := text(in.u())
		value := text(in.u())
		attrs[key] = value
	}
	count = in.count()
	var fields []field
	for i := 0; i < count && in.err == nil; i++ {
		if f := r.element(c, strings, depth+1); f != nil {
			fields = append(fields, *f)
		}
	}
	attr := func(key string) string {
		value, ok := attrs[key]
		if !ok {
			in.fail(invalid("metadata " + name + " missing " + key))
		}
		return value
	}
	parse := func(s string) uint64 {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			in.fail(invalid("invalid metadata integer"))
		}
		return n
	}
	if in.err != nil {
		return nil
	}
	if name == "class" {
		id := parse(attr("id"))
		typeName := attr("name")
		if in.err == nil {
			c.types[id] = typ{name: typeName, simple: attrs["simpleType"] == "true", fields: fields}
		}
	} else if name == "field" {
		typeID := parse(attr("class"))
		fieldName := attr("name")
		dimension := uint64(0)
		if s, ok := attrs["dimension"]; ok {
			dimension = parse(s)
		}
		if dimension > 128 {
			in.fail(invalid("metadata array dimension too deep"))
			return nil
		}
		return &field{name: fieldName, typeID: typeID, constantPool: attrs["constantPool"] == "true", dimension: int(dimension)}
	}
	return nil
}
func (r *Reader) constants(c *Chunk, offset uint64) {
	in := &r.in
	visited := make(map[uint64]bool)
	for in.err == nil {
		if offset < c.offset+headerSize || visited[offset] {
			in.fail(invalid("invalid/cyclic checkpoint chain"))
			return
		}
		visited[offset] = true
		kind, _ := in.record(offset, c.end)
		if kind != 1 {
			in.fail(invalid("checkpoint event type"))
			return
		}
		in.u()
		in.u()
		delta := int64(in.u())
		in.b()
		pools := in.count()
		for i := 0; i < pools && in.err == nil; i++ {
			id := in.u()
			t, ok := c.types[id]
			if !ok {
				in.fail(invalid("unknown constant-pool type"))
				return
			}
			r.pool(c, t)
		}
		if delta == 0 {
			return
		}
		if delta < 0 {
			back := uint64(-(delta + 1)) + 1
			if back > offset {
				in.fail(invalid("checkpoint offset overflow"))
				return
			}
			offset -= back
		} else {
			if uint64(delta) > ^uint64(0)-offset {
				in.fail(invalid("checkpoint offset overflow"))
				return
			}
			offset += uint64(delta)
		}
	}
}
func (r *Reader) cpEntry(c *Chunk, isNew bool) bool {
	if isNew {
		if err := c.reserve(96); err != nil {
			r.in.fail(err)
			return false
		}
	}
	return r.in.err == nil
}
func (r *Reader) pool(c *Chunk, t typ) {
	in := &r.in
	if t.name == "jdk.types.ChunkHeader" {
		var b [71]byte
		in.take(b[:])
		return
	}
	count := in.count()
	if in.err != nil {
		return
	}
	// A stack constant requires at least ID, truncated flag and depth. Check
	// before Java reference-order preallocation can reserve a large table.
	if t.name == "jdk.types.StackTrace" && uint64(count) > (in.limit-in.position())/3 {
		in.fail(invalid("stack pool count exceeds record payload"))
		return
	}
	if t.name == "jdk.types.StackTrace" && r.TrackStackOrder {
		in.fail(r.order.preallocate(count, c))
	}
	for i := 0; i < count && in.err == nil; i++ {
		id := in.u()
		if id == javaUsedBit {
			switch t.name {
			case "java.lang.Thread", "java.lang.Class", "java.lang.String", "jdk.types.Symbol", "jdk.types.Method", "jdk.types.StackTrace":
				in.fail(invalid("constant-pool key is reserved by Java Dictionary"))
				return
			}
		}
		switch t.name {
		case "java.lang.Thread":
			if _, exists := c.threads[id]; exists {
				in.discardString(c)
				in.u()
				in.discardString(c)
				in.u()
				r.extra(c, t, 4)
				continue
			}
			osName, osOK := in.refString(c.strings, &c.text)
			in.u()
			javaName, javaOK := in.refString(c.strings, &c.text)
			in.u()
			r.extra(c, t, 4)
			if _, ok := c.threads[id]; !ok && (javaOK || osOK) && r.cpEntry(c, true) {
				if javaOK {
					c.threads[id] = javaName
				} else {
					c.threads[id] = osName
				}
			}
		case "java.lang.Class":
			in.u()
			name := in.u()
			in.u()
			in.u()
			r.extra(c, t, 4)
			if _, ok := c.classes[id]; !ok && r.cpEntry(c, true) {
				c.classes[id] = ClassRef{Name: name}
			}
		case "java.lang.String":
			if _, exists := c.strings[id]; exists {
				in.discardString(c)
				continue
			}
			ref, valid := in.refString(c.strings, &c.text)
			if _, ok := c.strings[id]; !ok && valid && r.cpEntry(c, true) {
				c.strings[id] = ref
			}
		case "jdk.types.Symbol":
			if _, exists := c.symbols[id]; exists {
				in.discardString(c)
				continue
			}
			ref, _ := in.refString(c.strings, &c.text)
			if _, ok := c.symbols[id]; !ok && r.cpEntry(c, true) {
				c.symbols[id] = ref
			}
		case "jdk.types.Method":
			class, name := in.u(), in.u()
			in.u()
			in.u()
			in.u()
			r.extra(c, t, 5)
			if _, ok := c.methods[id]; !ok && r.cpEntry(c, true) {
				c.methods[id] = MethodRef{Class: class, Name: name}
			}
		case "jdk.types.StackTrace":
			in.b()
			depth := in.count()
			if depth > 65536 || uint64(depth) > (in.limit-in.position())/4 {
				in.fail(invalid("invalid or excessive stack depth"))
				return
			}
			if r.TrackStackOrder {
				in.fail(r.order.put(id, c))
			}
			if in.err != nil {
				return
			}
			_, exists := c.stacks[id]
			var ref stackRef
			if !exists {
				var err error
				ref, err = c.allocStack(depth)
				in.fail(err)
			}
			for j := 0; j < depth && in.err == nil; j++ {
				f := StackFrame{Method: in.u(), Line: int32(in.u()), BCI: int32(in.u()), Kind: in.b()}
				if !exists && in.err == nil {
					in.fail(c.setFrame(ref.start+uint64(j), f))
				}
			}
			if !exists && r.cpEntry(c, true) {
				c.stacks[id] = ref
			}
		default:
			if t.simple && len(t.fields) == 1 {
				if values := c.Enums[t.name]; values != nil {
					if _, exists := values[id]; exists {
						in.discardString(c)
						continue
					}
				}
				ref, _ := in.refString(c.strings, &c.text)
				values := c.Enums[t.name]
				if values == nil {
					values = make(map[uint64]string)
					c.Enums[t.name] = values
				}
				if _, ok := values[id]; !ok && r.cpEntry(c, true) {
					values[id] = c.text.string(ref)
				}
			} else {
				for _, f := range t.fields {
					r.skip(c, f, 0)
				}
			}
		}
	}
}
func (r *Reader) extra(c *Chunk, t typ, start int) {
	for i := start; i < len(t.fields) && r.in.err == nil; i++ {
		r.skip(c, t.fields[i], 0)
	}
}
func (r *Reader) skip(c *Chunk, f field, depth int) {
	in := &r.in
	if in.err != nil {
		return
	}
	if depth >= 128 {
		in.fail(invalid("field nesting too deep"))
		return
	}
	if f.dimension > 0 {
		count := in.count()
		f.dimension--
		for i := 0; i < count && in.err == nil; i++ {
			r.skip(c, f, depth+1)
		}
		return
	}
	if f.constantPool {
		in.u()
		return
	}
	t, ok := c.types[f.typeID]
	if !ok {
		in.fail(invalid("unknown field type"))
		return
	}
	switch t.name {
	case "java.lang.String":
		in.valueString(c)
	case "float":
		var b [4]byte
		in.take(b[:])
	case "double":
		var b [8]byte
		in.take(b[:])
	case "boolean", "byte":
		in.b()
	case "char", "short", "int", "long":
		in.u()
	default:
		if len(t.fields) == 0 {
			in.u()
		} else {
			for _, child := range t.fields {
				r.skip(c, child, depth+1)
			}
		}
	}
}

func (r *Reader) Events(c *Chunk, mask Mask, visit func(*Event) error) error {
	if r.closed {
		return os.ErrClosed
	}
	in := &r.in
	for offset := c.offset + headerSize; offset < c.end; {
		id, end := in.record(offset, c.end)
		if in.err != nil {
			return in.err
		}
		enc := encUnknown
		if id < uint64(len(c.encodings)) {
			enc = c.encodings[id]
		} else {
			enc = encodingFor(c.types[id].name)
		}
		if enc == encSetting {
			r.activeSetting(c, c.types[id])
			if in.err != nil {
				return in.err
			}
		} else if kind, ok := enc.kind(); ok && mask&(1<<kind) != 0 {
			r.event = Event{Kind: kind, Samples: 1, Value: 1}
			if enc == encCPUTime {
				r.cpuTime()
			} else if kind == Execution {
				r.execution(enc == encWall, enc == encWall && c.wallTimeSpan, end)
			} else {
				r.decode(c, c.types[id], enc)
			}
			if in.err != nil {
				return in.err
			}
			if visit != nil {
				if err := visit(&r.event); err != nil {
					return err
				}
			}
		}
		offset = end
	}
	return in.err
}
func (r *Reader) execution(wall, timeSpan bool, end uint64) {
	in, e := &r.in, &r.event
	e.Wall = wall
	lookahead := 45
	if timeSpan {
		lookahead += 9
	}
	if in.filled-in.cursor >= lookahead {
		at := in.cursor
		e.Time, at = bufferedVar(in.buffer, at)
		e.TID, at = bufferedVar(in.buffer, at)
		e.StackID, at = bufferedVar(in.buffer, at)
		e.State, at = bufferedVar(in.buffer, at)
		if wall {
			e.Samples, at = bufferedVar(in.buffer, at)
		}
		if timeSpan {
			_, at = bufferedVar(in.buffer, at)
		}
		if in.base+uint64(at) > end {
			in.fail(invalid("truncated execution sample"))
			return
		}
		in.cursor = at
	} else {
		e.Time = in.u()
		e.TID = in.u()
		e.StackID = in.u()
		e.State = in.u()
		if wall {
			e.Samples = in.u()
		}
		if timeSpan {
			in.u()
		}
	}
	e.Value = e.Samples
}
func (r *Reader) cpuTime() {
	in, e := &r.in, &r.event
	e.Time = in.u()
	e.StackID = in.u()
	e.TID = in.u()
	in.b() // failed
	in.u() // samplingPeriod
	in.b() // biased
	e.State = CPUTimeSampleState
}

func periodSetting(name string) bool {
	switch name {
	case "interval", "wall", "alloc", "lock", "nativelock", "nativemem":
		return true
	}
	return false
}

func (r *Reader) activeSetting(c *Chunk, t typ) {
	var name, value string
	for _, f := range t.fields {
		switch f.name {
		case "name":
			name = r.in.valueString(c)
		case "value":
			if periodSetting(name) {
				value = r.in.valueString(c)
			} else {
				r.in.discardString(c)
			}
		default:
			r.skip(c, f, 0)
		}
	}
	if r.in.err != nil || !periodSetting(name) {
		return
	}
	if err := c.reserve(uint64(len(value))); err != nil {
		r.in.fail(err)
		return
	}
	if r.settings == nil {
		r.settings = make(map[string]string)
	}
	oldBytes := uint64(len(r.settings[name]))
	c.reserved -= oldBytes
	r.settingsBytes -= oldBytes
	r.settings[name] = value
	r.settingsBytes += uint64(len(value))
	c.Settings = r.settings
}

func (r *Reader) decode(c *Chunk, t typ, enc encoding) {
	in, e := &r.in, &r.event
	if e.Kind == Span || e.Kind == MethodTrace {
		for _, f := range t.fields {
			if in.err != nil {
				return
			}
			if e.Kind == Span && f.name == "tag" && c.types[f.typeID].name == "java.lang.String" {
				if f.constantPool {
					id := in.u()
					ref, ok := c.strings[id]
					if ok {
						e.Tag = c.text.string(ref)
						e.TagPresent = true
					} else if id != 0 {
						in.fail(invalid("missing span tag constant"))
						return
					}
				} else {
					e.Tag, e.TagPresent = in.valueStringPresent(c)
				}
			} else {
				switch f.name {
				case "startTime", "time":
					e.Time = in.u()
				case "duration":
					e.Duration = in.u()
					e.Value = e.Duration
				case "eventThread", "sampledThread", "thread":
					e.TID = in.u()
				case "stackTrace":
					e.StackID = in.u()
				case "method":
					e.MethodID = in.u()
				default:
					r.skip(c, f, 0)
				}
			}
		}
		return
	}
	e.Time = in.u()
	if e.Kind == Lock || e.Kind == NativeLock {
		e.Duration = in.u()
		e.Value = e.Duration
	}
	e.TID = in.u()
	e.StackID = in.u()
	switch e.Kind {
	case NativeLock:
		e.Address = in.u()
	case Allocation:
		e.ClassID = in.u()
		e.Value = in.u()
		if enc == encTLAB {
			size := in.u()
			if size != 0 {
				e.TLAB = true
				e.Value = size
			}
		}
	case Live:
		e.ClassID = in.u()
		e.Value = in.u()
		in.u()
	case Lock:
		e.ClassID = in.u()
		if enc == encPark {
			in.u()
		}
		in.u()
		e.Address = in.u()
	case Malloc:
		e.Address = in.u()
		e.Value = in.u()
	case Free:
		e.Address = in.u()
		e.Value = 0
	}
}

// javaOrder models one.jfr.Dictionary key positions, including preallocate and
// resize history. Clear preserves capacity, as Java's per-chunk stack clear does.
type javaOrder struct {
	keys []uint64
	size int
}

const javaUsedBit uint64 = 1 << 63

func javaHash(key uint64) uint32 { key *= 0xc6a4a7935bd1e995; return uint32(key ^ (key >> 32)) }
func (o *javaOrder) clear()      { clear(o.keys); o.size = 0 }
func (o *javaOrder) resize(n int, c *Chunk) error {
	if err := c.reserve(uint64(n) * 8); err != nil {
		return err
	}
	next := make([]uint64, n)
	mask := n - 1
	for _, key := range o.keys {
		if key != 0 {
			at := int(javaHash(key)) & mask
			for next[at] != 0 {
				at = (at + 1) & mask
			}
			next[at] = key
		}
	}
	o.keys = next
	return nil
}
func (o *javaOrder) preallocate(n int, c *Chunk) error {
	if len(o.keys) == 0 {
		if err := o.resize(16, c); err != nil {
			return err
		}
	}
	if n*2 > len(o.keys) {
		capacity := 16
		for capacity < n*2 {
			capacity *= 2
		}
		return o.resize(capacity, c)
	}
	return nil
}
func (o *javaOrder) put(key uint64, c *Chunk) error {
	if key == javaUsedBit {
		return invalid("constant-pool stack key is reserved by Java Dictionary")
	}
	key ^= javaUsedBit
	if len(o.keys) == 0 {
		if err := o.resize(16, c); err != nil {
			return err
		}
	}
	mask := len(o.keys) - 1
	at := int(javaHash(key)) & mask
	for o.keys[at] != 0 {
		if o.keys[at] == key {
			return nil
		}
		at = (at + 1) & mask
	}
	o.keys[at] = key
	o.size++
	if o.size*2 > len(o.keys) {
		return o.resize(len(o.keys)*2, c)
	}
	return nil
}
