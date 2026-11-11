// Portions derived from async-profiler 4.1 and jfrconv-rs.
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"math"
	"os"
	"strings"
	"unicode/utf8"
	"unsafe"
)

const slabItems = 1024

// Guards owned major stores, not all Go allocations or the process RSS.
type dataBudget struct{ limit, owned uint64 }

func (b *dataBudget) reserve(n uint64) {
	if b == nil {
		return
	}
	if n > math.MaxUint64-b.owned || b.limit != 0 && n > b.limit-b.owned {
		panic(ErrMemoryBudget)
	}
	b.owned += n
}
func (b *dataBudget) release(n uint64) {
	if b != nil {
		b.owned -= n
	}
}

// Numeric slabs never copy their existing contents when a profile grows.
// Their elements contain no Go pointers, so GC scans only the short slab list.
type slabs[T any] struct {
	blocks [][]T
	n      uint32
	budget *dataBudget
}

func (s *slabs[T]) at(i uint32) *T { return &s.blocks[i/slabItems][i%slabItems] }
func (s *slabs[T]) add(v T) uint32 {
	if s.n == math.MaxUint32 {
		panic(ErrMemoryBudget)
	}
	if s.n%slabItems == 0 {
		var item T
		s.budget.reserve(uint64(slabItems) * uint64(unsafe.Sizeof(item)))
		s.blocks = append(s.blocks, make([]T, slabItems))
	}
	i := s.n
	*s.at(i) = v
	s.n++
	return i
}

type stringRef struct{ block, offset, length uint32 }
type stringPool struct {
	blocks [][]byte
	used   int
	refs   slabs[stringRef]
	slots  []uint32
	budget *dataBudget
}

func newStringPool(budget *dataBudget) stringPool {
	var s stringPool
	s.budget = budget
	s.refs.budget = budget
	s.refs.add(stringRef{})
	budget.reserve(16 * 4)
	s.slots = make([]uint32, 16)
	return s
}
func hashString(s string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint64(s[i])) * 1099511628211
	}
	return h
}
func (s *stringPool) text(i uint32) string {
	if i == 0 {
		return ""
	}
	r := s.refs.at(i)
	b := s.blocks[r.block][r.offset : r.offset+(r.length&0x7fffffff)]
	// Arena bytes are immutable after interning. The pool owns each fixed block.
	return unsafe.String(unsafe.SliceData(b), len(b))
}
func (s *stringPool) lookup(text string) (uint32, bool) {
	if text == "" {
		return 0, true
	}
	if len(s.slots) == 0 {
		return 0, false
	}
	mask := uint64(len(s.slots) - 1)
	at := hashString(text) & mask
	for {
		id := s.slots[at]
		if id == 0 {
			return 0, false
		}
		if s.text(id) == text {
			return id, true
		}
		at = (at + 1) & mask
	}
}
func (s *stringPool) intern(text string) uint32 {
	if id, ok := s.lookup(text); ok {
		return id
	}
	if uint64(len(text)) > math.MaxInt32 {
		panic(ErrMemoryBudget)
	}
	if len(s.blocks) == 0 || len(s.blocks[len(s.blocks)-1])-s.used < len(text) {
		size := 64 * 1024
		if len(text) > size {
			size = len(text)
		}
		s.budget.reserve(uint64(size))
		s.blocks = append(s.blocks, make([]byte, size))
		s.used = 0
	}
	block := len(s.blocks) - 1
	off := s.used
	copy(s.blocks[block][off:], text)
	s.used += len(text)
	length := uint32(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] >= 128 {
			length |= 1 << 31
			break
		}
	}
	id := s.refs.add(stringRef{uint32(block), uint32(off), length})
	if len(s.slots) == 0 {
		s.budget.reserve(16 * 4)
		s.slots = make([]uint32, 16)
	}
	if int(s.refs.n)*2 > len(s.slots) {
		oldBytes := uint64(len(s.slots)) * 4
		s.budget.reserve(oldBytes * 2)
		s.slots = make([]uint32, len(s.slots)*2)
		for n := uint32(1); n < s.refs.n; n++ {
			s.insert(n)
		}
		s.budget.release(oldBytes)
	} else {
		s.insert(id)
	}
	return id
}
func (s *stringPool) insert(id uint32) {
	mask := uint64(len(s.slots) - 1)
	at := hashString(s.text(id)) & mask
	for s.slots[at] != 0 {
		at = (at + 1) & mask
	}
	s.slots[at] = id
}
func (s *stringPool) compare(a, b uint32) int {
	if (s.refs.at(a).length|s.refs.at(b).length)&(1<<31) == 0 {
		return strings.Compare(s.text(a), s.text(b))
	}
	return compareUTF16(s.text(a), s.text(b))
}

type frame struct {
	Name       uint32
	Kind, Role uint8
	Line, BCI  int32
}
type node struct {
	Parent, Frame                     uint32
	Total, Self, Samples, Value, Time uint64
}
type tierCounts struct{ Inlined, C1, Interpreted uint64 }
type tierAt struct {
	Node uint32
	Kind uint8
}
type cachedPath struct {
	Leaf  uint32
	Tiers []tierAt
}

type profile struct {
	budget                     *dataBudget
	heat                       *heatReference
	strings                    stringPool
	frames                     slabs[frame]
	nodes                      slabs[node]
	frameSlots, edgeSlots      []uint32
	tiers                      map[uint32]tierCounts
	originalDepth              int
	htmlEncounter              bool
	sourceTitles               []uint32
	isDiff                     bool
	diffs                      map[uint32]int64
	StartNS, EndNS             uint64
	ValueType, Unit, TotalUnit string
	spool                      *os.File
	scratch                    []string
	options                    Options
}

func newProfile(o Options) *profile {
	budget := &dataBudget{limit: o.MemoryLimit}
	budget.reserve(2 * 16 * 4)
	p := &profile{budget: budget, strings: newStringPool(budget), frameSlots: make([]uint32, 16), edgeSlots: make([]uint32, 16), options: o, ValueType: o.ValueType(), Unit: "count", TotalUnit: o.TotalUnit()}
	p.frames.budget = budget
	p.nodes.budget = budget
	if o.Total {
		p.Unit = p.TotalUnit
	}
	p.frames.add(frame{Kind: 3})
	p.nodes.add(node{})
	p.insertFrame(0)
	return p
}
func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	return x ^ (x >> 33)
}
func frameHash(f frame) uint64 {
	return mix64(uint64(f.Name)|uint64(f.Kind)<<32|uint64(f.Role)<<40) ^ mix64(uint64(uint32(f.Line))<<32|uint64(uint32(f.BCI)))
}
func (p *profile) insertFrame(id uint32) {
	mask := uint64(len(p.frameSlots) - 1)
	at := frameHash(*p.frames.at(id)) & mask
	for p.frameSlots[at] != 0 {
		at = (at + 1) & mask
	}
	p.frameSlots[at] = id + 1
}
func (p *profile) frameKey(f frame) uint32 {
	mask := uint64(len(p.frameSlots) - 1)
	at := frameHash(f) & mask
	for {
		n := p.frameSlots[at]
		if n == 0 {
			break
		}
		if *p.frames.at(n - 1) == f {
			return n - 1
		}
		at = (at + 1) & mask
	}
	id := p.frames.add(f)
	if int(p.frames.n)*2 > len(p.frameSlots) {
		oldBytes := uint64(len(p.frameSlots)) * 4
		p.budget.reserve(oldBytes * 2)
		p.frameSlots = make([]uint32, len(p.frameSlots)*2)
		for n := uint32(0); n < p.frames.n; n++ {
			p.insertFrame(n)
		}
		p.budget.release(oldBytes)
	} else {
		p.frameSlots[at] = id + 1
	}
	return id
}
func (p *profile) frameName(name string, kind uint8, line, bci int32, role uint8) uint32 {
	return p.frameKey(frame{Name: p.strings.intern(name), Kind: kind, Role: role, Line: line, BCI: bci})
}
func (p *profile) insertEdge(id uint32) {
	n := p.nodes.at(id)
	mask := uint64(len(p.edgeSlots) - 1)
	at := mix64(uint64(n.Parent)<<32|uint64(n.Frame)) & mask
	for p.edgeSlots[at] != 0 {
		at = (at + 1) & mask
	}
	p.edgeSlots[at] = id
}
func (p *profile) child(parent, f uint32) uint32 {
	mask := uint64(len(p.edgeSlots) - 1)
	at := mix64(uint64(parent)<<32|uint64(f)) & mask
	for {
		id := p.edgeSlots[at]
		if id == 0 {
			break
		}
		n := p.nodes.at(id)
		if n.Parent == parent && n.Frame == f {
			return id
		}
		at = (at + 1) & mask
	}
	id := p.nodes.add(node{Parent: parent, Frame: f})
	if int(p.nodes.n)*2 > len(p.edgeSlots) {
		oldBytes := uint64(len(p.edgeSlots)) * 4
		p.budget.reserve(oldBytes * 2)
		p.edgeSlots = make([]uint32, len(p.edgeSlots)*2)
		for n := uint32(1); n < p.nodes.n; n++ {
			p.insertEdge(n)
		}
		p.budget.release(oldBytes)
	} else {
		p.edgeSlots[at] = id
	}
	return id
}
func (p *profile) path(ids []uint32, normalize bool) cachedPath {
	if len(ids) > p.originalDepth {
		p.originalDepth = len(ids)
	}
	var path cachedPath
	for _, id := range ids {
		f := *p.frames.at(id)
		kind := f.Kind
		normalized := id
		if normalize {
			if kind == 0 || kind == 2 || kind == 6 {
				f.Kind = 1
			}
			normalized = p.frameKey(f)
		}
		path.Leaf = p.child(path.Leaf, normalized)
		if normalize && (kind == 0 || kind == 2 || kind == 6) {
			path.Tiers = append(path.Tiers, tierAt{path.Leaf, kind})
		}
	}
	return path
}
func satAdd(a, b uint64) uint64 {
	c := a + b
	if c < a {
		return math.MaxUint64
	}
	return c
}
func (p *profile) credit(path cachedPath, amount, samples, value, t uint64) {
	n := p.nodes.at(path.Leaf)
	n.Self = satAdd(n.Self, amount)
	n.Samples = satAdd(n.Samples, samples)
	n.Value = satAdd(n.Value, value)
	if n.Time == 0 || t < n.Time {
		n.Time = t
	}
	for id := path.Leaf; ; {
		n = p.nodes.at(id)
		n.Total = satAdd(n.Total, amount)
		if id == 0 {
			break
		}
		id = n.Parent
	}
	if len(path.Tiers) > 0 && p.tiers == nil {
		p.tiers = make(map[uint32]tierCounts)
	}
	for _, at := range path.Tiers {
		c, exists := p.tiers[at.Node]
		if !exists {
			p.budget.reserve(64)
		}
		switch at.Kind {
		case 0:
			c.Interpreted = satAdd(c.Interpreted, amount)
		case 2:
			c.Inlined = satAdd(c.Inlined, amount)
		case 6:
			c.C1 = satAdd(c.C1, amount)
		}
		p.tiers[at.Node] = c
	}
}
func (p *profile) kind(id uint32) uint8 {
	n := p.nodes.at(id)
	c := p.tiers[id]
	if c.Inlined >= ceilDiv(n.Total, 3) {
		return 2
	}
	if c.C1 >= ceilDiv(n.Total, 2) {
		return 6
	}
	if c.Interpreted >= ceilDiv(n.Total, 2) {
		return 0
	}
	return p.frames.at(n.Frame).Kind
}
func ceilDiv(n, d uint64) uint64 { return n/d + uint64(boolInt(n%d != 0)) }
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func (p *profile) seal() {
	p.budget.release(uint64(len(p.frameSlots)+len(p.edgeSlots)+len(p.strings.slots)) * 4)
	p.frameSlots = nil
	p.edgeSlots = nil
	p.strings.slots = nil
}
func (p *profile) readerBudget() (uint64, error) {
	if p.budget.limit == 0 {
		return 0, nil
	}
	if p.budget.owned >= p.budget.limit {
		return 0, ErrMemoryBudget
	}
	return p.budget.limit - p.budget.owned, nil
}
func (p *profile) close() {
	if p.spool != nil {
		p.spool.Close()
	}
	for _, name := range p.scratch {
		os.Remove(name)
	}
}
func (p *profile) temp() (*os.File, error) {
	f, err := os.CreateTemp(p.options.TempDir, "jfrconv-go-*")
	if err == nil {
		p.scratch = append(p.scratch, f.Name())
	}
	return f, err
}

// Java compares UTF-16 code units; UTF-8 byte order differs for supplementary
// characters. ASCII names avoid decoding, while non-ASCII needs no allocation.
type utf16Iter struct {
	s       string
	pending uint16
}

func (i *utf16Iter) next() (uint16, bool) {
	if i.pending != 0 {
		x := i.pending
		i.pending = 0
		return x, true
	}
	if i.s == "" {
		return 0, false
	}
	r, n := utf8.DecodeRuneInString(i.s)
	i.s = i.s[n:]
	if r > 0xffff {
		r -= 0x10000
		i.pending = uint16(0xdc00 + (r & 1023))
		return uint16(0xd800 + (r >> 10)), true
	}
	return uint16(r), true
}
func compareUTF16(a, b string) int {
	if a == b {
		return 0
	}
	x, y := utf16Iter{s: a}, utf16Iter{s: b}
	for {
		u, ok := x.next()
		v, yes := y.next()
		if !ok {
			if yes {
				return -1
			}
			return 0
		}
		if !yes {
			return 1
		}
		if u < v {
			return -1
		}
		if u > v {
			return 1
		}
	}
}
