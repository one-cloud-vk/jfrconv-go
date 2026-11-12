// Copyright The async-profiler authors
// Copyright 2026 jfrconv-rs and jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"fmt"
	"io"
	"jfrconv-go/internal/jfr"
	"os"
)

const heatUnknown = ^uint32(0)

type heatMethodKey struct {
	Class, Method, Location uint32
	Kind                    uint8
}
type heatReference struct {
	frames             map[heatMethodKey]uint32
	keys               []heatMethodKey
	methodRanks        [][2]uint32
	stackRanks, leaves []uint32
	nextMethod         uint32
	extra              *os.File
	extraOut           *bufio.Writer
}

func newHeatReference() *heatReference {
	return &heatReference{frames: make(map[heatMethodKey]uint32), keys: make([]heatMethodKey, 1)}
}
func (p *profile) heatFrame(display, class, method uint32, kind uint8, line, bci int32) uint32 {
	key := heatMethodKey{class, method, uint32(uint16(line))<<16 | uint32(uint16(bci)), kind}
	if id, ok := p.heat.frames[key]; ok {
		return id
	}
	// Conservative selected-identity map/vector allowance, without a large
	// fixed allocation for recordings with only a few methods.
	p.budget.reserve(80)
	id := p.frames.add(frame{Name: display, Kind: kind, Line: line, BCI: bci})
	p.heat.frames[key] = id
	p.heat.keys = append(p.heat.keys, key)
	return id
}
func (p *profile) lookupExactPath(frames []uint32) (uint32, bool) {
	parent := uint32(0)
	for _, f := range frames {
		mask := uint64(len(p.edgeSlots) - 1)
		at := mix64(uint64(parent)<<32|uint64(f)) & mask
		found := false
		for {
			v := p.edgeSlots[at]
			if v == 0 {
				break
			}
			n := p.nodes.at(v)
			if n.Parent == parent && n.Frame == f {
				parent = v
				found = true
				break
			}
			at = (at + 1) & mask
		}
		if !found {
			return 0, false
		}
	}
	return parent, true
}
func (r *heatReference) recordExtra(chunk uint64, leaf uint32, dir string) error {
	if r.extra == nil {
		f, e := heatTemp(dir)
		if e != nil {
			return e
		}
		r.extra = f
		r.extraOut = bufio.NewWriterSize(f, 64*1024)
	}
	return heatPut(r.extraOut, heatRecord{chunk, uint64(leaf)})
}
func (r *heatReference) method(frame uint32, start bool) {
	at := 0
	if start {
		at = 1
	}
	if r.methodRanks[frame][at] == heatUnknown {
		r.methodRanks[frame][at] = r.nextMethod
		r.nextMethod++
	}
}
func (r *heatReference) stack(leaf uint32) {
	if r.stackRanks[leaf] == heatUnknown {
		r.stackRanks[leaf] = uint32(len(r.leaves))
		r.leaves = append(r.leaves, leaf)
	}
}
func heatParts(c *jfr.Chunk, id uint64, kind uint8, o Options) (string, string) {
	m, ok := c.Method(id)
	if !ok {
		return "", "unknown"
	}
	class := ""
	if ref, ok := c.Class(m.Class); ok {
		class = c.Symbol(ref.Name)
	}
	_, ap := c.Enums["jdk.types.FrameType"][5]
	if kind == 4 || kind == 5 || kind == 3 && ap {
		class = ""
	} else {
		class = javaName(class, false, o.Dot, o)
	}
	return class, c.Symbol(m.Name)
}

type heatExtra struct {
	Chunk uint64
	Leaf  uint32
}

func heatReadExtra(r io.Reader) (heatExtra, bool, error) {
	v, ok, e := heatRawRead(r)
	if e != nil || !ok {
		return heatExtra{}, ok, e
	}
	if v.Count > uint64(^uint32(0)) {
		return heatExtra{}, false, fmt.Errorf("invalid heatmap extra leaf")
	}
	return heatExtra{v.Key, uint32(v.Count)}, true, nil
}

func (h *exactHeatmap) referenceOrder() error {
	p, r := h.p, h.ref
	p.budget.reserve(uint64(p.frames.n)*8 + uint64(p.nodes.n)*4)
	r.methodRanks = make([][2]uint32, p.frames.n)
	for i := range r.methodRanks {
		r.methodRanks[i] = [2]uint32{heatUnknown, heatUnknown}
	}
	r.stackRanks = make([]uint32, p.nodes.n)
	for i := range r.stackRanks {
		r.stackRanks[i] = heatUnknown
	}
	var extraReader *bufio.Reader
	var next heatExtra
	have := false
	if r.extraOut != nil {
		if e := r.extraOut.Flush(); e != nil {
			return e
		}
		r.extraOut = nil
		if _, e := r.extra.Seek(0, io.SeekStart); e != nil {
			return e
		}
		extraReader = bufio.NewReaderSize(r.extra, 64*1024)
		var e error
		next, have, e = heatReadExtra(extraReader)
		if e != nil {
			return e
		}
	}
	reader, e := jfr.Open(h.path)
	if e != nil {
		return e
	}
	defer reader.Close()
	reader.TrackStackOrder = true
	chunkIndex := uint64(0)
	var frames []uint32
	for {
		available, budgetError := p.readerBudget()
		if budgetError != nil {
			return budgetError
		}
		reader.Budget = available
		c, e := reader.NextChunk()
		if e == io.EOF {
			break
		}
		if e != nil {
			return parserError(e)
		}
		type pair struct {
			Class, Method uint32
			Known         bool
		}
		parts := make(map[methodKey]pair)
		for _, id := range c.StackOrder {
			stack := c.Stack(id)
			frames = frames[:0]
			complete := true
			for i := stack.Len() - 1; i >= 0; i-- {
				f := stack.At(i)
				cacheKey := methodKey{f.Method, f.Kind}
				names, ok := parts[cacheKey]
				if !ok {
					class, method := heatParts(c, f.Method, f.Kind, h.o)
					a, yes := p.strings.lookup(class)
					b, also := p.strings.lookup(method)
					names = pair{a, b, yes && also}
					parts[cacheKey] = names
				}
				key := heatMethodKey{names.Class, names.Method, uint32(uint16(f.Line))<<16 | uint32(uint16(f.BCI)), f.Kind}
				frame, ok := r.frames[key]
				if names.Known && ok {
					r.method(frame, i == stack.Len()-1 && !h.o.Threads)
					frames = append(frames, frame)
				} else {
					complete = false
				}
			}
			if complete {
				if leaf, ok := p.lookupExactPath(frames); ok && leaf != 0 && p.nodes.at(leaf).Self != 0 {
					r.stack(leaf)
				}
			}
		}
		for have && next.Chunk == chunkIndex {
			if next.Leaf >= p.nodes.n {
				return fmt.Errorf("invalid heatmap extra chronology")
			}
			frames = frames[:0]
			for at := next.Leaf; at != 0; at = p.nodes.at(at).Parent {
				frames = append(frames, at)
			}
			for i := len(frames) - 1; i >= 0; i-- {
				n := p.nodes.at(frames[i])
				r.method(n.Frame, n.Parent == 0)
			}
			r.stack(next.Leaf)
			next, have, e = heatReadExtra(extraReader)
			if e != nil {
				return e
			}
		}
		chunkIndex++
	}
	if have {
		return fmt.Errorf("invalid heatmap chunk chronology")
	}
	for id := uint32(1); id < p.nodes.n; id++ {
		if p.nodes.at(id).Self != 0 && r.stackRanks[id] == heatUnknown {
			r.stack(id)
		}
	}
	r.frames = nil
	if r.extra != nil {
		r.extra.Close()
		r.extra = nil
	}
	return nil
}
