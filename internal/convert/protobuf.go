// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
// Protobuf schemas follow the converter bundled with async-profiler v4.5.
package convert

import (
	"bufio"
	"compress/gzip"
	"math"
)

// pbOut streams known-size messages. No serialized profile or nested-message
// byte slices are constructed, and varints do not allocate temporary buffers.
type pbOut struct {
	out *bufio.Writer
	err error
}

func (p *pbOut) v(n uint64) {
	if p.err != nil {
		return
	}
	for n >= 128 {
		if p.err = p.out.WriteByte(byte(n) | 128); p.err != nil {
			return
		}
		n >>= 7
	}
	p.err = p.out.WriteByte(byte(n))
}
func (p *pbOut) u(field uint64, n uint64) { p.v(field << 3); p.v(n) }
func (p *pbOut) h(field uint64, n uint64) { p.v(field<<3 | 2); p.v(n) }
func (p *pbOut) s(field uint64, s string) {
	p.h(field, uint64(len(s)))
	if p.err == nil {
		_, p.err = p.out.WriteString(s)
	}
}
func pbVSize(n uint64) uint64 {
	size := uint64(1)
	for n >= 128 {
		n >>= 7
		size++
	}
	return size
}
func pbMSize(field, length uint64) uint64 { return pbVSize(field<<3|2) + pbVSize(length) + length }
func pbUSize(field, value uint64) uint64  { return pbVSize(field<<3) + pbVSize(value) }
func difference(end, start uint64) uint64 {
	if end < start {
		return 0
	}
	return end - start
}

func writePprof(p *profile, out *bufio.Writer, _ Options) error {
	q := pbOut{out: out}
	usedFrames := make([]bool, p.frames.n)
	usedNames := make([]bool, p.strings.refs.n)
	for id := uint32(1); id < p.nodes.n; id++ {
		fID := p.nodes.at(id).Frame
		f := p.frames.at(fID)
		if f.Role == 0 {
			usedFrames[fID] = true
			usedNames[f.Name] = true
		}
	}
	base := uint64(p.strings.refs.n) + 1
	q.h(1, pbUSize(1, base)+pbUSize(2, base+1))
	q.u(1, base)
	q.u(2, base+1)
	q.u(13, base+3)
	for id := uint32(0); id < p.nodes.n; id++ {
		n := p.nodes.at(id)
		if n.Self == 0 && n.Samples == 0 && n.Value == 0 {
			continue
		}
		packed, labelSize := uint64(0), uint64(0)
		for at := id; at != 0; at = p.nodes.at(at).Parent {
			fID := p.nodes.at(at).Frame
			f := p.frames.at(fID)
			if f.Role == 0 {
				packed += pbVSize(uint64(fID) + 1)
			} else if f.Role == 1 || f.Role == 2 {
				key := base + 5
				if f.Role == 2 {
					key++
				}
				labelSize += pbMSize(3, pbUSize(1, key)+pbUSize(2, uint64(f.Name)+1))
			}
		}
		q.h(2, pbMSize(1, packed)+pbUSize(2, n.Self)+labelSize)
		q.h(1, packed)
		for at := id; at != 0; at = p.nodes.at(at).Parent {
			fID := p.nodes.at(at).Frame
			if p.frames.at(fID).Role == 0 {
				q.v(uint64(fID) + 1)
			}
		}
		q.u(2, n.Self)
		for at := id; at != 0; at = p.nodes.at(at).Parent {
			f := p.frames.at(p.nodes.at(at).Frame)
			if f.Role == 1 || f.Role == 2 {
				key := base + 5
				if f.Role == 2 {
					key++
				}
				q.h(3, pbUSize(1, key)+pbUSize(2, uint64(f.Name)+1))
				q.u(1, key)
				q.u(2, uint64(f.Name)+1)
			}
		}
	}
	q.h(3, pbUSize(1, 1)+pbUSize(2, 0)+pbUSize(3, math.MaxInt64)+pbUSize(5, base+4))
	q.u(1, 1)
	q.u(2, 0)
	q.u(3, math.MaxInt64)
	q.u(5, base+4)
	for id := uint32(1); id < p.frames.n; id++ {
		if !usedFrames[id] {
			continue
		}
		f := p.frames.at(id)
		line := uint64(max(f.Line, 0))
		fn := uint64(f.Name) + 1
		lineSize := pbUSize(1, fn) + pbUSize(2, line)
		q.h(4, pbUSize(1, uint64(id)+1)+pbMSize(4, lineSize))
		q.u(1, uint64(id)+1)
		q.h(4, lineSize)
		q.u(1, fn)
		q.u(2, line)
	}
	for id, used := range usedNames {
		if used {
			fn := uint64(id) + 1
			q.h(5, pbUSize(1, fn)+pbUSize(2, fn))
			q.u(1, fn)
			q.u(2, fn)
		}
	}
	q.s(6, "")
	for id := uint32(0); id < p.strings.refs.n; id++ {
		q.s(6, p.strings.text(id))
	}
	for _, s := range []string{p.ValueType, p.Unit, p.TotalUnit, "Produced by jfrconv-go (async-profiler 4.5 compatible)", "jfrconv-go", "thread", "category"} {
		q.s(6, s)
	}
	q.u(9, p.StartNS)
	q.u(10, difference(p.EndNS, p.StartNS))
	return q.err
}
func writePprofGzip(p *profile, out *bufio.Writer, o Options) error {
	z, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		return err
	}
	// Coalesce protobuf's byte writes before invoking the compressor.
	buffer := bufio.NewWriterSize(z, 64*1024)
	err = writePprof(p, buffer, o)
	if err == nil {
		err = buffer.Flush()
	}
	closeErr := z.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
