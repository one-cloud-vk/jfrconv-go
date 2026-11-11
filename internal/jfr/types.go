// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0

// Package jfr reads JFR 2.x recordings without retaining event payloads.
package jfr

import "errors"

type EventKind uint8

const (
	Execution EventKind = iota
	Allocation
	Live
	Lock
	Malloc
	Free
	Span
)

type Mask uint8

const (
	MaskExecution  Mask = 1 << Execution
	MaskAllocation Mask = 1 << Allocation
	MaskLive       Mask = 1 << Live
	MaskLock       Mask = 1 << Lock
	MaskNative     Mask = 1<<Malloc | 1<<Free
	MaskSpan       Mask = 1 << Span
	MaskAll        Mask = 127
)

type ClassRef struct{ Name uint64 }
type MethodRef struct{ Class, Name uint64 }
type StackFrame struct {
	Method    uint64
	Kind      uint8
	Line, BCI int32
}

// Event is reused by Reader.Events. A callback must copy values it retains.
type Event struct {
	Kind                                                                            EventKind
	Time, TID, StackID, MethodID, ClassID, State, Samples, Value, Address, Duration uint64
	TLAB, Wall, TagPresent                                                          bool
	Tag                                                                             string
}

var ErrBudget = errors.New("JFR constant-pool memory budget exceeded")

type strRef struct{ block, offset, length uint32 }
type stackRef struct {
	start  uint64
	length uint32
}

const stackSlabSize = 1024

// Keep upper method bits in a separate column only for slabs that need them.
// Locations retain all 32 bits of both line and BCI; no values are narrowed by
// this representation.
type frameSlab struct {
	// Keeping the only pointer first limits the GC's pointer prefix to one
	// word; the large frame columns do not need scanning.
	high      *[stackSlabSize]uint32
	methods   [stackSlabSize]uint32
	locations [stackSlabSize]uint64
	kinds     [stackSlabSize]uint8
}

// StackTrace is a read-only view of a chunk's frames. It remains valid while
// the chunk is retained. Missing and present-but-empty stacks both have length
// zero; use Chunk.HasStack when their identities must be distinguished.
type StackTrace struct {
	chunk  *Chunk
	start  uint64
	length uint32
}

func (s StackTrace) Len() int { return int(s.length) }

func (s StackTrace) At(index int) StackFrame {
	if uint(index) >= uint(s.length) {
		panic("stack frame index out of range")
	}
	position := s.start + uint64(index)
	slab := s.chunk.frames[position/stackSlabSize]
	slot := position % stackSlabSize
	method := uint64(slab.methods[slot])
	if slab.high != nil {
		method |= uint64(slab.high[slot]) << 32
	}
	location := slab.locations[slot]
	return StackFrame{Method: method, Kind: slab.kinds[slot], Line: int32(location), BCI: int32(location >> 32)}
}

type field struct {
	name         string
	typeID       uint64
	constantPool bool
	dimension    int
}
type typ struct {
	name   string
	simple bool
	fields []field
}

// Chunk owns its constant pools. Stack frames and text use segmented flat
// arenas, avoiding one Go allocation per stack and pointer-rich CP map values.
type Chunk struct {
	StartNS, EndNS, StartTicks, TicksPerSec uint64
	Enums                                   map[string]map[uint64]string
	StackOrder                              []uint64
	threads                                 map[uint64]strRef
	classes                                 map[uint64]ClassRef
	symbols                                 map[uint64]strRef
	methods                                 map[uint64]MethodRef
	stacks                                  map[uint64]stackRef
	strings                                 map[uint64]strRef
	types                                   map[uint64]typ
	encodings                               []encoding
	text                                    byteArena
	frames                                  []*frameSlab
	frameCount                              uint64
	budget, reserved                        uint64
	offset, end                             uint64
}

func (c *Chunk) Stack(id uint64) StackTrace {
	r, ok := c.stacks[id]
	if !ok || r.length == 0 {
		return StackTrace{}
	}
	return StackTrace{chunk: c, start: r.start, length: r.length}
}
func (c *Chunk) HasStack(id uint64) bool            { _, ok := c.stacks[id]; return ok }
func (c *Chunk) Method(id uint64) (MethodRef, bool) { v, ok := c.methods[id]; return v, ok }
func (c *Chunk) Class(id uint64) (ClassRef, bool)   { v, ok := c.classes[id]; return v, ok }
func (c *Chunk) Symbol(id uint64) string            { return c.text.string(c.symbols[id]) }
func (c *Chunk) ThreadName(id uint64) string        { return c.text.string(c.threads[id]) }

func (c *Chunk) reserve(n uint64) error {
	if n > ^uint64(0)-c.reserved || c.budget != 0 && n > c.budget-c.reserved {
		return ErrBudget
	}
	c.reserved += n
	return nil
}

type byteArena struct {
	blocks [][]byte
	owner  *Chunk
	exact  bool
}

func (a *byteArena) alloc(n int) (strRef, []byte, error) {
	if n == 0 {
		return strRef{}, nil, nil
	}
	if len(a.blocks) == 0 || cap(a.blocks[len(a.blocks)-1])-len(a.blocks[len(a.blocks)-1]) < n {
		capacity := 32 * 1024
		if a.exact {
			capacity = n
		}
		if n > capacity {
			capacity = n
		}
		if a.owner != nil {
			if err := a.owner.reserve(uint64(capacity)); err != nil {
				return strRef{}, nil, err
			}
		}
		a.blocks = append(a.blocks, make([]byte, 0, capacity))
	}
	last := len(a.blocks) - 1
	start := len(a.blocks[last])
	a.blocks[last] = a.blocks[last][:start+n]
	return strRef{uint32(last), uint32(start), uint32(n)}, a.blocks[last][start : start+n], nil
}
func (a *byteArena) bytes(r strRef) []byte {
	if r.length == 0 {
		return nil
	}
	return a.blocks[r.block][r.offset : uint64(r.offset)+uint64(r.length)]
}
func (a *byteArena) string(r strRef) string { return string(a.bytes(r)) }
func (a *byteArena) store(s string) (strRef, error) {
	r, b, e := a.alloc(len(s))
	copy(b, s)
	return r, e
}

func (c *Chunk) allocStack(n int) (stackRef, error) {
	if n == 0 {
		return stackRef{}, nil
	}
	end := c.frameCount + uint64(n)
	if n < 0 || end < c.frameCount {
		return stackRef{}, ErrBudget
	}
	for uint64(len(c.frames)) < (end-1)/stackSlabSize+1 {
		if err := c.addFrameSlab(); err != nil {
			return stackRef{}, err
		}
	}
	ref := stackRef{start: c.frameCount, length: uint32(n)}
	c.frameCount = end
	return ref, nil
}

func (c *Chunk) addFrameSlab() error {
	// A slab has 13 bytes per frame plus its optional-column pointer. Eight
	// bytes per pointer includes alignment on 64-bit Go and is conservative
	// on 32-bit targets. Reserve the new index before releasing its old array
	// so the budget also covers the copy's peak storage.
	storage := uint64(stackSlabSize*13 + 8)
	capacity := cap(c.frames)
	if len(c.frames) == capacity {
		if capacity > int(^uint(0)>>1)/2 {
			return ErrBudget
		}
		capacity *= 2
		if capacity == 0 {
			capacity = 1
		}
		storage += uint64(capacity) * 8
	}
	if err := c.reserve(storage); err != nil {
		return err
	}
	if capacity != cap(c.frames) {
		frames := make([]*frameSlab, len(c.frames), capacity)
		copy(frames, c.frames)
		c.reserved -= uint64(cap(c.frames)) * 8
		c.frames = frames
	}
	c.frames = append(c.frames, new(frameSlab))
	return nil
}

func (c *Chunk) setFrame(position uint64, frame StackFrame) error {
	slab := c.frames[position/stackSlabSize]
	slot := position % stackSlabSize
	if frame.Method>>32 != 0 && slab.high == nil {
		if err := c.reserve(stackSlabSize * 4); err != nil {
			return err
		}
		slab.high = new([stackSlabSize]uint32)
	}
	slab.methods[slot] = uint32(frame.Method)
	if slab.high != nil {
		slab.high[slot] = uint32(frame.Method >> 32)
	}
	slab.locations[slot] = uint64(uint32(frame.Line)) | uint64(uint32(frame.BCI))<<32
	slab.kinds[slot] = frame.Kind
	return nil
}
