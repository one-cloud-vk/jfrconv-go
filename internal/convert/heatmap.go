// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"jfrconv-go/assets"
	"jfrconv-go/internal/jfr"
	"math"
	"math/bits"
	"os"
	"sort"
)

type heatCacheKey struct {
	Stack, Class uint64
	TLAB         bool
}
type heatNames struct{ Display, Class, Method uint32 }
type heatGroupKey struct {
	MS   uint64
	Leaf uint32
}
type heatGroup struct{ Count, Value uint64 }
type exactHeatmap struct {
	path       string
	p          *profile
	o          Options
	ref        *heatReference
	spool      *os.File
	out        *bufio.Writer
	cache      map[heatCacheKey]uint32
	names      map[methodKey]heatNames
	groups     map[heatGroupKey]heatGroup
	origin     uint64
	hasOrigin  bool
	chunk      uint64
	pathFrames []uint32
}

func init() {
	makeHeatmap = func(path string, p *profile, o Options) (heatmapSession, error) {
		f, e := heatTemp(o.TempDir)
		if e != nil {
			return nil, e
		}
		r := newHeatReference()
		p.heat = r
		return &exactHeatmap{path: path, p: p, o: o, ref: r, spool: f, out: bufio.NewWriterSize(f, 128*1024), groups: make(map[heatGroupKey]heatGroup)}, nil
	}
}
func (h *exactHeatmap) BeforeChunk(*jfr.Chunk) error {
	h.cache = make(map[heatCacheKey]uint32)
	h.names = make(map[methodKey]heatNames)
	return nil
}
func (h *exactHeatmap) AfterChunk(*jfr.Chunk) error {
	if e := h.flush(); e != nil {
		return e
	}
	h.cache = nil
	h.names = nil
	h.chunk++
	return nil
}
func (h *exactHeatmap) Close() {
	if h.spool != nil {
		h.spool.Close()
		h.spool = nil
	}
	if h.ref.extra != nil {
		h.ref.extra.Close()
		h.ref.extra = nil
	}
}
func (h *exactHeatmap) flush() error {
	for key, value := range h.groups {
		if e := h.record(key.MS, key.Leaf, value.Count, value.Value); e != nil {
			return e
		}
	}
	clear(h.groups)
	return nil
}
func (h *exactHeatmap) record(ms uint64, leaf uint32, count, value uint64) error {
	if e := h.rawRecord(ms, leaf, count, value); e != nil {
		return e
	}
	h.p.credit(cachedPath{Leaf: leaf}, count, count, value, ms*1_000_000)
	return nil
}
func (h *exactHeatmap) rawRecord(ms uint64, leaf uint32, count, value uint64) error {
	if h.out.Available() < 32 {
		if e := h.out.Flush(); e != nil {
			return e
		}
	}
	b := h.out.AvailableBuffer()
	for _, v := range [4]uint64{ms * 1_000_000, uint64(leaf), count, value} {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	if _, e := h.out.Write(b); e != nil {
		return e
	}
	return nil
}

// Native leak resolution happens while the owning chunk is still alive. The
// core credits only surviving addresses; Retained therefore writes no counters.
func (h *exactHeatmap) ResolveRetained(c *jfr.Chunk, e *jfr.Event) (cachedPath, bool, error) {
	if h.names == nil {
		h.names = make(map[methodKey]heatNames)
	}
	leaf, err := h.resolve(c, e)
	return cachedPath{Leaf: leaf}, leaf != 0, err
}
func (h *exactHeatmap) Retained(leaf uint32, count, value, timeNS uint64) error {
	if leaf == 0 || leaf >= h.p.nodes.n || count == 0 {
		return fmt.Errorf("invalid retained heatmap stack")
	}
	return h.rawRecord(timeNS/1_000_000, leaf, count, value)
}
func heatMS(c *jfr.Chunk, ticks uint64) uint64 {
	base := c.StartNS / 1_000_000
	if ticks >= c.StartTicks {
		return satAdd(base, mulDiv(ticks-c.StartTicks, 1000, c.TicksPerSec))
	}
	delta := mulDiv(c.StartTicks-ticks, 1000, c.TicksPerSec)
	if delta > base {
		return 0
	}
	return base - delta
}
func (h *exactHeatmap) resolve(c *jfr.Chunk, e *jfr.Event) (uint32, error) {
	if !c.HasStack(e.StackID) {
		return 0, nil
	}
	stack := c.Stack(e.StackID)
	ids := h.pathFrames[:0]
	for i := len(stack) - 1; i >= 0; i-- {
		f := stack[i]
		key := methodKey{f.Method, f.Kind}
		names, ok := h.names[key]
		if !ok {
			class, method := heatParts(c, f.Method, f.Kind, h.o)
			display := methodName(c, f.Method, f.Kind, h.o)
			names = heatNames{h.p.strings.intern(display), h.p.strings.intern(class), h.p.strings.intern(method)}
			h.names[key] = names
		}
		ids = append(ids, h.p.heatFrame(names.Display, names.Class, names.Method, f.Kind, int32(uint16(f.Line)), int32(uint16(f.BCI))))
	}
	if e.ClassID != 0 && (e.Kind == jfr.Allocation || e.Kind == jfr.Lock) {
		class := h.p.strings.intern(className(c, e.ClassID, h.o))
		kind := uint8(5)
		if e.Kind == jfr.Allocation && e.TLAB {
			kind = 2
		}
		ids = append(ids, h.p.heatFrame(class, class, 0, kind, -1, -1))
	}
	h.pathFrames = ids
	leaf := h.p.path(ids, false).Leaf
	if leaf != 0 && e.ClassID != 0 && (e.Kind == jfr.Allocation || e.Kind == jfr.Lock) {
		if err := h.ref.recordExtra(h.chunk, leaf, h.o.TempDir); err != nil {
			return 0, err
		}
	}
	return leaf, nil
}
func (h *exactHeatmap) Add(c *jfr.Chunk, e *jfr.Event) error {
	key := heatCacheKey{e.StackID, e.ClassID, e.TLAB}
	leaf, ok := h.cache[key]
	if !ok {
		var err error
		leaf, err = h.resolve(c, e)
		if err != nil {
			return err
		}
		h.cache[key] = leaf
	}
	if leaf == 0 {
		return nil
	}
	ms := heatMS(c, e.Time)
	value := normalizeValue(e.Value, c, h.o)
	if !h.hasOrigin {
		h.hasOrigin = true
		h.origin = ms
		return h.record(ms, leaf, 1, value)
	}
	block := (int64(ms) - int64(h.origin)) / 20
	canonical := uint64(int64(h.origin) + block*20)
	groupKey := heatGroupKey{canonical, leaf}
	group := h.groups[groupKey]
	if group.Count == heatLimit {
		return fmt.Errorf("heatmap sample count overflow")
	}
	group.Count++
	group.Value = satAdd(group.Value, value)
	h.groups[groupKey] = group
	if len(h.groups) >= 8192 {
		return h.flush()
	}
	return nil
}
func (h *exactHeatmap) Finish() error {
	if e := h.out.Flush(); e != nil {
		return e
	}
	h.out = nil
	h.groups = nil
	h.cache = nil
	h.names = nil
	h.pathFrames = nil
	return h.referenceOrder()
}

type heatTree struct {
	children    []uint8
	parts       []uint32
	starts      map[uint32]uint32
	startFrames []uint32
}

func newHeatTree(p *profile) (heatTree, error) {
	t := heatTree{children: make([]uint8, p.nodes.n), parts: make([]uint32, p.nodes.n), starts: make(map[uint32]uint32)}
	for id := uint32(1); id < p.nodes.n; id++ {
		n := p.nodes.at(id)
		if n.Parent >= id {
			return t, fmt.Errorf("invalid heatmap trie parent")
		}
		if t.children[n.Parent] < 2 {
			t.children[n.Parent]++
		}
	}
	for id := uint32(1); id < p.nodes.n; id++ {
		n := p.nodes.at(id)
		t.parts[id] = t.parts[n.Parent]
		if n.Parent == 0 || t.children[n.Parent] > 1 {
			t.parts[id]++
		}
		if n.Parent == 0 {
			t.starts[n.Frame] = 0
		}
	}
	for f := range t.starts {
		t.startFrames = append(t.startFrames, f)
	}
	sort.Slice(t.startFrames, func(i, j int) bool { return t.startFrames[i] < t.startFrames[j] })
	for i, f := range t.startFrames {
		t.starts[f] = p.frames.n + uint32(i)
	}
	return t, nil
}
func (t *heatTree) pieces(p *profile, leaf uint32, parts []uint32) []uint32 {
	parts = append(parts[:0], leaf)
	for {
		parent := p.nodes.at(leaf).Parent
		if parent == 0 {
			break
		}
		if t.children[parent] > 1 {
			parts = append(parts, parent)
		}
		leaf = parent
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return parts
}

type heatPrepared struct {
	keys, counts            *os.File
	values                  []uint32
	blocks, samples, chunks uint64
}

func (d *heatPrepared) close() {
	if d.keys != nil {
		d.keys.Close()
	}
	if d.counts != nil {
		d.counts.Close()
	}
}
func (h *exactHeatmap) prepare(t *heatTree) (d heatPrepared, err error) {
	stat, err := h.spool.Stat()
	if err != nil {
		return d, err
	}
	if stat.Size()%32 != 0 {
		return d, fmt.Errorf("truncated heatmap spool")
	}
	if _, err = h.spool.Seek(0, io.SeekStart); err != nil {
		return d, err
	}
	input := bufio.NewReaderSize(h.spool, 64*1024)
	sorter := heatSorter{dir: h.o.TempDir}
	defer sorter.close()
	var first uint64
	haveFirst := false
	var raw [32]byte
	for i := int64(0); i < stat.Size()/32; i++ {
		if _, err = io.ReadFull(input, raw[:]); err != nil {
			return d, err
		}
		ms := binary.LittleEndian.Uint64(raw[:8]) / 1_000_000
		leaf := binary.LittleEndian.Uint64(raw[8:16])
		count := binary.LittleEndian.Uint64(raw[16:24])
		if leaf == 0 || leaf >= uint64(h.p.nodes.n) || count == 0 {
			return d, fmt.Errorf("invalid heatmap spool record")
		}
		d.samples, err = heatAdd(d.samples, count)
		if err != nil {
			return d, err
		}
		parts := uint64(t.parts[leaf])
		if count > heatLimit/parts {
			return d, fmt.Errorf("too many heatmap dictionary references")
		}
		d.chunks, err = heatAdd(d.chunks, parts*count)
		if err != nil {
			return d, err
		}
		if !haveFirst {
			first = ms
			haveFirst = true
		}
		block := (int64(ms) - int64(first)) / 20
		if block < math.MinInt32 || block > math.MaxInt32 {
			return d, fmt.Errorf("heatmap time range exceeds original format")
		}
		rank := h.ref.stackRanks[leaf]
		if rank == heatUnknown {
			return d, fmt.Errorf("missing heatmap reference stack rank")
		}
		key := uint64(uint32(int32(block))^0x80000000)<<32 | uint64(rank)
		if err = sorter.push(key, count); err != nil {
			return d, err
		}
	}
	d.keys, err = sorter.finish()
	if err != nil {
		return d, err
	}
	defer func() {
		if err != nil {
			d.close()
		}
	}()
	d.counts, err = heatTemp(h.o.TempDir)
	if err != nil {
		return d, err
	}
	in := bufio.NewReaderSize(d.keys, 64*1024)
	out := bufio.NewWriterSize(d.counts, 64*1024)
	distinct := make(map[uint32]struct{})
	var previous uint64
	var count uint64
	have := false
	put := func(value uint32) error {
		distinct[value] = struct{}{}
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], value)
		_, e := out.Write(b[:])
		return e
	}
	for {
		v, ok, e := heatRead(in)
		if e != nil {
			return d, e
		}
		if !ok {
			break
		}
		block := v.Key >> 32
		if have && block != previous {
			if err = put(uint32(count)); err != nil {
				return d, err
			}
			d.blocks++
			gap := block - previous - 1
			if d.blocks+gap > heatLimit {
				return d, fmt.Errorf("too many heatmap time blocks")
			}
			for i := uint64(0); i < gap; i++ {
				if err = put(0); err != nil {
					return d, err
				}
			}
			d.blocks += gap
			count = 0
		}
		previous = block
		have = true
		count, err = heatAdd(count, v.Count)
		if err != nil {
			return d, err
		}
	}
	if have {
		if err = put(uint32(count)); err != nil {
			return d, err
		}
		d.blocks++
	}
	if err = out.Flush(); err != nil {
		return d, err
	}
	for value := range distinct {
		d.values = append(d.values, value)
	}
	sort.Slice(d.values, func(i, j int) bool { return d.values[i] < d.values[j] })
	_, err = d.keys.Seek(0, io.SeekStart)
	return d, err
}

type heatMethods struct{ order, ids []uint32 }

func (h *exactHeatmap) ordered(t *heatTree, d *heatPrepared) (heatMethods, error) {
	count := h.p.frames.n - 1 + uint32(len(t.starts))
	m := heatMethods{make([]uint32, count), make([]uint32, count+1)}
	frequencies := make([]uint64, count+1)
	index := newHeatLZ(h.o.TempDir)
	defer index.close()
	if _, e := d.keys.Seek(0, io.SeekStart); e != nil {
		return m, e
	}
	input := bufio.NewReaderSize(d.keys, 64*1024)
	var path []uint32
	for {
		v, ok, e := heatRead(input)
		if e != nil {
			return m, e
		}
		if !ok {
			break
		}
		if int(uint32(v.Key)) >= len(h.ref.leaves) {
			return m, fmt.Errorf("invalid heatmap rank")
		}
		leaf := h.ref.leaves[uint32(v.Key)]
		path = path[:0]
		for leaf != 0 {
			n := h.p.nodes.at(leaf)
			method := n.Frame
			if n.Parent == 0 {
				method = t.starts[method]
			}
			path = append(path, method)
			leaf = n.Parent
		}
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		for n := uint64(0); n < v.Count; n++ {
			current := uint32(0)
			inserted := false
			for _, method := range path {
				current, e = index.append(current, method)
				if e != nil {
					return m, e
				}
				if current == 0 {
					frequencies[method]++
					inserted = true
				}
			}
			if !inserted {
				break
			}
		}
	}
	rank := func(method uint32) uint32 {
		if method < h.p.frames.n {
			return h.ref.methodRanks[method][0]
		}
		return h.ref.methodRanks[t.startFrames[method-h.p.frames.n]][1]
	}
	for i := range m.order {
		m.order[i] = uint32(i + 1)
	}
	sort.Slice(m.order, func(i, j int) bool {
		a, b := m.order[i], m.order[j]
		if frequencies[a] != frequencies[b] {
			return frequencies[a] > frequencies[b]
		}
		ra, rb := rank(a), rank(b)
		if ra != rb {
			return ra < rb
		}
		return a < b
	})
	for i, method := range m.order {
		m.ids[method] = uint32(i + 1)
	}
	_, e := d.keys.Seek(0, io.SeekStart)
	return m, e
}

type heatHTML struct {
	w   *bufio.Writer
	err error
}

func (h *heatHTML) byte(v uint8) {
	if h.err != nil {
		return
	}
	switch v {
	case 0:
		v = 127
	case 13:
		v = 126
	case 38:
		v = 125
	case 60:
		v = 124
	case 62:
		v = 123
	}
	h.err = h.w.WriteByte(v)
}
func (h *heatHTML) varint(v uint64) {
	for v >= 61 {
		h.byte(61 + uint8(v%61))
		v /= 61
	}
	h.byte(uint8(v))
}
func (h *heatHTML) int18(v uint32) {
	for i := 0; i < 3; i++ {
		h.byte(uint8(v & 63))
		v >>= 6
	}
}
func (h *heatHTML) int30(v uint64) {
	if v > heatLimit {
		h.err = fmt.Errorf("heatmap exceeds original 30-bit format")
		return
	}
	for i := 0; i < 5; i++ {
		h.byte(uint8(v & 63))
		v >>= 6
	}
}
func (h *heatHTML) bits27(v uint32) {
	h.byte(uint8(v / 1860867))
	h.byte(uint8(v / 15129 % 123))
	h.byte(uint8(v / 123 % 123))
	h.byte(uint8(v % 123))
}
func writeHeatBlocks(out *heatHTML, d *heatPrepared) error {
	size := len(d.values)
	width := 0
	if size > 1 {
		width = bits.Len(uint(size - 1))
	}
	out.varint(uint64(size))
	out.varint(uint64(width))
	for _, value := range d.values {
		out.varint(uint64(value))
		out.varint(uint64(width))
	}
	if width == 0 {
		return out.err
	}
	if _, e := d.counts.Seek(0, io.SeekStart); e != nil {
		return e
	}
	input := bufio.NewReaderSize(d.counts, 64*1024)
	var packed uint32
	used := 0
	var b [4]byte
	for i := uint64(0); i < d.blocks; i++ {
		if _, e := io.ReadFull(input, b[:]); e != nil {
			return e
		}
		v := binary.LittleEndian.Uint32(b[:])
		code := uint32(sort.Search(len(d.values), func(i int) bool { return d.values[i] >= v }))
		for shift := width - 1; shift >= 0; shift-- {
			packed |= ((code >> shift) & 1) << used
			used++
			if used == 27 {
				out.bits27(packed)
				packed = 0
				used = 0
			}
		}
	}
	if used != 0 {
		out.bits27(packed)
	}
	return out.err
}
func (h *exactHeatmap) Write(w *bufio.Writer) error {
	tree, e := newHeatTree(h.p)
	if e != nil {
		return e
	}
	d, e := h.prepare(&tree)
	if e != nil {
		return e
	}
	defer d.close()
	if d.samples == 0 {
		_, e = w.WriteString("No samples found\n")
		return e
	}
	if uint64(h.p.nodes.n) > heatLimit {
		return fmt.Errorf("too many heatmap trie nodes")
	}
	methods, e := h.ordered(&tree, &d)
	if e != nil {
		return e
	}
	t := template{assets.Heatmap, w}
	if e = t.until("/*executionsHeatmap:*/"); e != nil {
		return e
	}
	w.WriteByte('S')
	out := heatHTML{w: w}
	out.varint(uint64(len(tree.starts)))
	for _, frame := range tree.startFrames {
		out.varint(uint64(methods.ids[tree.starts[frame]]))
	}
	if e = writeHeatBlocks(&out, &d); e != nil {
		return e
	}
	out.varint(0)
	for id := uint32(1); id < h.p.nodes.n; id++ {
		n := h.p.nodes.at(id)
		parent := n.Parent
		if tree.children[parent] > 1 {
			parent = 0
		}
		out.varint(uint64(parent))
		method := n.Frame
		if n.Parent == 0 {
			method = tree.starts[method]
		}
		out.varint(uint64(methods.ids[method]))
	}
	out.varint(0)
	input := bufio.NewReaderSize(d.keys, 64*1024)
	var previous uint32
	var pieces []uint32
	for {
		v, ok, e := heatRead(input)
		if e != nil {
			return e
		}
		if !ok {
			break
		}
		leaf := h.ref.leaves[uint32(v.Key)]
		if leaf != previous {
			pieces = tree.pieces(h.p, leaf, pieces)
			previous = leaf
		}
		for i := uint64(0); i < v.Count; i++ {
			for _, part := range pieces {
				out.varint(uint64(part))
			}
		}
	}
	out.int30(uint64(h.p.nodes.n))
	out.int30(d.blocks)
	out.int30(uint64(h.p.nodes.n - 1))
	out.int30(d.chunks)
	out.int30(d.samples)
	if out.err != nil {
		return out.err
	}
	w.WriteByte('E')
	if e = t.until("/*methods:*/"); e != nil {
		return e
	}
	w.WriteString("SA")
	out.varint(uint64(len(methods.order)))
	for _, method := range methods.order {
		frame := method
		if method >= h.p.frames.n {
			frame = tree.startFrames[method-h.p.frames.n]
		}
		key := h.ref.keys[frame]
		out.varint(uint64(key.Class))
		out.varint(uint64(key.Method))
		out.int18(key.Location & 0xffff)
		out.int18(key.Location >> 16)
		out.byte(key.Kind)
	}
	if out.err != nil {
		return out.err
	}
	w.WriteString("AE")
	if e = t.until("/*title:*/"); e != nil {
		return e
	}
	w.WriteString(h.o.Title)
	if e = t.until("/*startMs:*/0"); e != nil {
		return e
	}
	unsigned(w, h.p.StartNS/1_000_000)
	if e = t.until("/*cpool:*/"); e != nil {
		return e
	}
	for id := uint32(0); id < h.p.strings.refs.n; id++ {
		encoded, e := json.Marshal(h.p.strings.text(id))
		if e != nil {
			return e
		}
		if _, e = w.Write(encoded); e != nil {
			return e
		}
		w.WriteByte(',')
	}
	_, e = w.WriteString(t.tail)
	return e
}
