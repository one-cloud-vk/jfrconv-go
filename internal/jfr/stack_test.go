// SPDX-License-Identifier: Apache-2.0
package jfr

import (
	"errors"
	"math"
	"testing"
)

func TestStackColumnsKeepFullValuesAcrossSlabs(t *testing.T) {
	frames := make([]StackFrame, stackSlabSize+5)
	for i := range frames {
		frames[i] = StackFrame{Method: uint64(i + 1), Kind: uint8(i), Line: int32(i), BCI: -int32(i)}
	}
	boundaries := []StackFrame{
		{Method: math.MaxUint32, Kind: 0, Line: math.MinInt32, BCI: math.MaxInt32},
		{Method: math.MaxUint32 + 1, Kind: 1, Line: math.MaxInt32, BCI: math.MinInt32},
		{Method: 1<<63 | 42, Kind: 2, Line: -1, BCI: 65536},
		{Method: math.MaxUint64, Kind: math.MaxUint8, Line: 65536, BCI: -1},
		{Method: 7, Kind: 3, Line: 0, BCI: 0},
	}
	copy(frames[stackSlabSize:], boundaries)
	data := []byte{1, 42, 0}
	data = append(data, varBytes(uint64(len(frames)))...)
	for _, frame := range frames {
		data = append(data, varBytes(frame.Method)...)
		data = append(data, varBytes(uint64(int64(frame.Line)))...)
		data = append(data, varBytes(uint64(int64(frame.BCI)))...)
		data = append(data, frame.Kind)
	}
	r := Reader{in: testInput(t, data)}
	c := Chunk{stacks: make(map[uint64]stackRef)}
	r.pool(&c, typ{name: "jdk.types.StackTrace"})
	if r.in.err != nil {
		t.Fatal(r.in.err)
	}
	stack := c.Stack(42)
	if stack.Len() != len(frames) || len(c.frames) != 2 {
		t.Fatalf("frames=%d slabs=%d", stack.Len(), len(c.frames))
	}
	for i, want := range frames {
		if got := stack.At(i); got != want {
			t.Fatalf("frame %d: got %+v, want %+v", i, got, want)
		}
	}
	if c.frames[0].high != nil || c.frames[1].high == nil {
		t.Fatal("upper method bits were not allocated only for the wide slab")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		for i, want := range frames {
			if stack.At(i) != want {
				panic("frame changed")
			}
		}
	}); allocations != 0 {
		t.Fatalf("frame access allocated %g objects", allocations)
	}
}

func TestStackSlabsPackAdjacentTracesAndKeepViews(t *testing.T) {
	c := Chunk{stacks: make(map[uint64]stackRef)}
	a, err := c.allocStack(stackSlabSize - 1)
	if err != nil {
		t.Fatal(err)
	}
	c.stacks[1] = a
	if err = c.setFrame(a.start, StackFrame{Method: 99, Line: -1}); err != nil {
		t.Fatal(err)
	}
	first := c.Stack(1)
	b, err := c.allocStack(3)
	if err != nil {
		t.Fatal(err)
	}
	c.stacks[2] = b
	if b.start != stackSlabSize-1 || c.frameCount != stackSlabSize+2 || len(c.frames) != 2 {
		t.Fatalf("adjacent traces left unused slots: start=%d count=%d slabs=%d", b.start, c.frameCount, len(c.frames))
	}
	for i := 0; i < 3; i++ {
		if err = c.setFrame(b.start+uint64(i), StackFrame{Method: uint64(i + 1), BCI: int32(i)}); err != nil {
			t.Fatal(err)
		}
		if got := c.Stack(2).At(i); got.Method != uint64(i+1) || got.BCI != int32(i) {
			t.Fatal(got)
		}
	}
	if first.At(0) != (StackFrame{Method: 99, Line: -1}) {
		t.Fatal("growing the slab index changed a retained view")
	}
}

func TestStackSlabBudgetGuardsEveryAllocation(t *testing.T) {
	const base = stackSlabSize*13 + 8 + 8 // columns, optional pointer, first index entry
	c := Chunk{budget: base - 1}
	if _, err := c.allocStack(1); !errors.Is(err, ErrBudget) || len(c.frames) != 0 || cap(c.frames) != 0 || c.reserved != 0 {
		t.Fatalf("base allocation escaped budget: err=%v reserved=%d", err, c.reserved)
	}
	c.budget = base + stackSlabSize*4 - 1
	ref, err := c.allocStack(1)
	if err != nil || c.reserved != base {
		t.Fatalf("base allocation: %v reserved=%d", err, c.reserved)
	}
	wide := StackFrame{Method: math.MaxUint64}
	if err = c.setFrame(ref.start, wide); !errors.Is(err, ErrBudget) || c.frames[0].high != nil || c.reserved != base {
		t.Fatalf("wide allocation escaped budget: err=%v reserved=%d", err, c.reserved)
	}
	c.budget++
	if err = c.setFrame(ref.start, wide); err != nil || c.reserved != c.budget {
		t.Fatalf("wide allocation: %v reserved=%d", err, c.reserved)
	}
	// Growing the pointer index needs the new array while the old one still
	// exists, in addition to the next slab. Reject before either is allocated.
	before := c.reserved
	c.budget = before + stackSlabSize*13 + 8 + 16 - 1
	if _, err = c.allocStack(stackSlabSize); !errors.Is(err, ErrBudget) || len(c.frames) != 1 || c.frameCount != 1 || c.reserved != before {
		t.Fatalf("index growth escaped budget: err=%v frames=%d reserved=%d", err, c.frameCount, c.reserved)
	}
}
