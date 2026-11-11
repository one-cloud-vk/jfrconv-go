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
type stackRef struct{ block, offset, length uint32 }
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
	frames                                  [][]StackFrame
	budget, reserved                        uint64
	offset, end                             uint64
}

func (c *Chunk) Stack(id uint64) []StackFrame {
	r, ok := c.stacks[id]
	if !ok || r.length == 0 {
		return nil
	}
	return c.frames[r.block][r.offset : uint64(r.offset)+uint64(r.length)]
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

func (c *Chunk) allocStack(n int) (stackRef, []StackFrame, error) {
	if n == 0 {
		return stackRef{}, nil, nil
	}
	if len(c.frames) == 0 || cap(c.frames[len(c.frames)-1])-len(c.frames[len(c.frames)-1]) < n {
		capacity := 1024
		if n > capacity {
			capacity = n
		}
		if err := c.reserve(uint64(capacity) * 24); err != nil {
			return stackRef{}, nil, err
		}
		c.frames = append(c.frames, make([]StackFrame, 0, capacity))
	}
	last := len(c.frames) - 1
	start := len(c.frames[last])
	c.frames[last] = c.frames[last][:start+n]
	return stackRef{uint32(last), uint32(start), uint32(n)}, c.frames[last][start : start+n], nil
}
