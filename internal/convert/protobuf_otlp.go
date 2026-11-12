// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
// Field numbers and event semantics match async-profiler v4.5 (11aaea310cb7),
// not the different development schema in later async-profiler revisions.
package convert

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
)

// Raw records are [timestamp, leaf, selected value, tid32|stack32, thread name].
// Chunk headers are [start, MaxUint64, end, period, 0]. Event timestamps and
// values remain on disk: neither a large group nor many chunks require arrays.
type otlpRecord [5]uint64

const otlpRecordBytes = 40
const otlpIndexBytes = 56

type otlpChunk struct {
	start, end, dataStart, dataEnd, period, encodedSize uint64
	writeValues                                         bool
}

func readOTLPRecord(reader io.Reader, storage *[otlpRecordBytes]byte) (otlpRecord, error) {
	if _, err := io.ReadFull(reader, storage[:]); err != nil {
		return otlpRecord{}, err
	}
	return decodeOTLPRecord(storage), nil
}
func decodeOTLPRecord(storage *[otlpRecordBytes]byte) (record otlpRecord) {
	for i := range record {
		record[i] = binary.LittleEndian.Uint64(storage[i*8 : i*8+8])
	}
	return record
}
func writeOTLPRecord(writer io.Writer, record otlpRecord) error {
	var storage [otlpRecordBytes]byte
	for i, n := range record {
		binary.LittleEndian.PutUint64(storage[i*8:i*8+8], n)
	}
	_, err := writer.Write(storage[:])
	return err
}
func eachOTLPRecord(file *os.File, chunk otlpChunk, reader *bufio.Reader, visit func(otlpRecord) error) error {
	if _, err := file.Seek(int64(chunk.dataStart), io.SeekStart); err != nil {
		return err
	}
	reader.Reset(file)
	var storage [otlpRecordBytes]byte
	for at := chunk.dataStart; at < chunk.dataEnd; at += otlpRecordBytes {
		record, err := readOTLPRecord(reader, &storage)
		if err != nil {
			return err
		}
		if err = visit(record); err != nil {
			return err
		}
	}
	return nil
}
func (c otlpChunk) bytes() (b [otlpIndexBytes]byte) {
	for i, n := range [7]uint64{c.start, c.end, c.dataStart, c.dataEnd, c.period, uint64(boolInt(c.writeValues)), c.encodedSize} {
		binary.LittleEndian.PutUint64(b[i*8:], n)
	}
	return b
}
func readOTLPChunk(index *os.File, i uint64) (otlpChunk, error) {
	var b [otlpIndexBytes]byte
	if _, err := index.ReadAt(b[:], int64(i*otlpIndexBytes)); err != nil {
		return otlpChunk{}, err
	}
	return otlpChunk{
		start: binary.LittleEndian.Uint64(b[:]), end: binary.LittleEndian.Uint64(b[8:]),
		dataStart: binary.LittleEndian.Uint64(b[16:]), dataEnd: binary.LittleEndian.Uint64(b[24:]),
		period: binary.LittleEndian.Uint64(b[32:]), writeValues: binary.LittleEndian.Uint64(b[40:]) != 0,
		encodedSize: binary.LittleEndian.Uint64(b[48:]),
	}, nil
}
func (p *pbOut) fixed64(n uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	p.raw(b[:])
}
func (p *pbOut) raw(b []byte) {
	if p.err == nil {
		_, p.err = p.out.Write(b)
	}
}

type countedWriter struct {
	writer io.Writer
	n      uint64
}

func (c *countedWriter) Write(b []byte) (int, error) {
	n, err := c.writer.Write(b)
	c.n += uint64(n)
	return n, err
}

type otlpDictionary struct {
	// IDs refer to compact global dictionary tables; zero is always the
	// schema-required empty/default entry, never a real synthetic root frame.
	stacks, locations, functions  []uint32
	locationFrames, functionNames []uint32
	attributes                    []uint32
	attributeIDs                  []uint32
	budget                        *dataBudget
	owned                         uint64
}

func makeOTLPDictionary(p *profile) otlpDictionary {
	d := otlpDictionary{budget: p.budget}
	// Keys are already bounded interned string IDs; a dense pointer-free
	// attribute index avoids implicitly growing Go map storage.
	d.reserve(4*(uint64(p.nodes.n)+uint64(p.frames.n)+2*uint64(p.strings.refs.n)) + 3*4)
	d.stacks = make([]uint32, p.nodes.n)
	d.locations = make([]uint32, p.frames.n)
	d.functions = make([]uint32, p.strings.refs.n)
	d.attributeIDs = make([]uint32, p.strings.refs.n)
	d.locationFrames, d.functionNames, d.attributes = []uint32{0}, []uint32{0}, []uint32{0}
	for i := range d.stacks {
		d.stacks[i] = math.MaxUint32
	}
	for i := range d.locations {
		d.locations[i] = math.MaxUint32
	}
	d.stacks[0], d.locations[0] = 0, 0
	return d
}
func (d *otlpDictionary) reserve(bytes uint64) {
	d.budget.reserve(bytes)
	d.owned += bytes
}
func (d *otlpDictionary) release(bytes uint64) {
	d.budget.release(bytes)
	d.owned -= bytes
}
func (d *otlpDictionary) close() {
	d.budget.release(d.owned)
	d.owned = 0
}
func (d *otlpDictionary) appendID(dst *[]uint32, value uint32) {
	if len(*dst) == cap(*dst) {
		oldCapacity := cap(*dst)
		capacity := max(16, oldCapacity*2)
		if capacity < oldCapacity {
			panic(ErrMemoryBudget)
		}
		// Keep the old charge until the new allocation is copied/replaced.
		d.reserve(uint64(capacity) * 4)
		next := make([]uint32, len(*dst), capacity)
		copy(next, *dst)
		*dst = next
		d.release(uint64(oldCapacity) * 4)
	}
	*dst = append(*dst, value)
}
func (d *otlpDictionary) addEvent(p *profile, r otlpRecord) {
	if d.attributeIDs[uint32(r[4])] == 0 {
		d.attributeIDs[uint32(r[4])] = uint32(len(d.attributes))
		d.appendID(&d.attributes, uint32(r[4]))
	}
	leaf := uint32(r[1])
	if d.stacks[leaf] != math.MaxUint32 {
		return
	}
	d.stacks[leaf] = 0
	for at := leaf; at != 0; at = p.nodes.at(at).Parent {
		f := p.nodes.at(at).Frame
		if p.frames.at(f).Role == 0 {
			d.locations[f] = 0
		}
	}
}
func (d *otlpDictionary) finish(p *profile) {
	next := uint32(1)
	for i := 1; i < len(d.stacks); i++ {
		if d.stacks[i] != math.MaxUint32 {
			d.stacks[i] = next
			next++
		}
	}
	// Frame kind and BCI do not distinguish OTLP locations in v4.5: Java's
	// Line.equals compares only the resolved function name and source line.
	// The conservative allowance includes old/new bucket storage at growth.
	mapBytes := uint64(256)
	d.reserve(mapBytes)
	defer func() { d.release(mapBytes) }()
	lineIDs := map[uint64]uint32{0: 0}
	for i := uint32(1); i < p.frames.n; i++ {
		if d.locations[i] == math.MaxUint32 {
			continue
		}
		f := p.frames.at(i)
		key := uint64(f.Name)<<32 | uint64(uint32(max(f.Line, 0)))
		location, ok := lineIDs[key]
		if !ok {
			d.reserve(96)
			mapBytes += 96
			location = uint32(len(d.locationFrames))
			lineIDs[key] = location
			d.appendID(&d.locationFrames, i)
		}
		d.locations[i] = location
		if f.Name != 0 && d.functions[f.Name] == 0 {
			d.functions[f.Name] = uint32(len(d.functionNames))
			d.appendID(&d.functionNames, f.Name)
		}
	}
}
func writeOTLPDictionary(p *profile, q *pbOut, d *otlpDictionary) {
	q.h(1, 0) // default mapping_table[0]
	q.h(4, pbMSize(1, 16)+pbMSize(2, 8))
	q.h(1, 16)
	q.raw(make([]byte, 16))
	q.h(2, 8)
	q.raw(make([]byte, 8)) // link_table[0] has zero trace/span IDs
	for _, name := range d.functionNames {
		q.h(3, pbUSize(1, uint64(name)))
		q.u(1, uint64(name))
	}
	q.h(2, 0) // location_table[0] is empty, without a synthetic line
	for _, id := range d.locationFrames[1:] {
		f := p.frames.at(id)
		line := uint64(max(f.Line, 0))
		lineSize := pbUSize(1, uint64(d.functions[f.Name])) + pbUSize(2, line)
		q.h(2, pbUSize(1, 0)+pbMSize(3, lineSize))
		q.u(1, 0)
		q.h(3, lineSize)
		q.u(1, uint64(d.functions[f.Name]))
		q.u(2, line)
	}
	q.h(7, pbMSize(1, 0)) // stack_table[0] is the present empty stack
	q.h(1, 0)
	for leaf := uint32(1); leaf < p.nodes.n; leaf++ {
		if d.stacks[leaf] == math.MaxUint32 {
			continue
		}
		packed := uint64(0)
		for at := leaf; at != 0; at = p.nodes.at(at).Parent {
			frame := p.nodes.at(at).Frame
			if p.frames.at(frame).Role == 0 {
				packed += pbVSize(uint64(d.locations[frame]))
			}
		}
		q.h(7, pbMSize(1, packed))
		q.h(1, packed)
		for at := leaf; at != 0; at = p.nodes.at(at).Parent {
			frame := p.nodes.at(at).Frame
			if p.frames.at(frame).Role == 0 {
				q.v(uint64(d.locations[frame]))
			}
		}
	}
	for id := uint32(0); id < p.strings.refs.n; id++ {
		q.s(5, p.strings.text(id))
	}
	for _, s := range []string{p.ValueType, "count", p.TotalUnit, "thread.name"} {
		q.s(5, s)
	}
	q.h(6, 0) // attribute_table[0] is empty
	threadKey := uint64(p.strings.refs.n) + 3
	for _, name := range d.attributes[1:] {
		value := p.strings.text(name)
		q.h(6, pbUSize(1, threadKey)+pbMSize(2, pbMSize(1, uint64(len(value)))))
		q.u(1, threadKey)
		q.h(2, pbMSize(1, uint64(len(value))))
		q.s(1, value)
	}
}

func otlpDefaultPeriod(p *profile) uint64 {
	switch p.options.Mode {
	case ModeNative:
		return 1
	case ModeAllocation, ModeLive:
		return 524287
	case ModeLock, ModeNativeLock:
		return 10000
	}
	if p.options.Wall {
		return 50000000
	}
	return 10000000
}

// writeOTLP encodes v4.5's per-event arrays and global stack dictionary once
// into bounded disk spools. Only dictionary cardinality, not event/group/chunk
// count, contributes unbounded memory. Even one group with millions of events
// is emitted by streaming its timestamp and value columns separately.
func writeOTLP(p *profile, out *bufio.Writer, _ Options) error {
	if p.spool == nil {
		return errors.New("OTLP sample spool is missing")
	}
	info, err := p.spool.Stat()
	if err != nil {
		return err
	}
	bytes := uint64(info.Size())
	if bytes%otlpRecordBytes != 0 {
		return errors.New("truncated OTLP sample spool")
	}
	if _, err = p.spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	index, err := p.temp()
	if err != nil {
		return err
	}
	defer index.Close()
	indexOut := bufio.NewWriterSize(index, 64*1024)
	dictionary := makeOTLPDictionary(p)
	defer dictionary.close()
	var chunk otlpChunk
	hasChunk := false
	chunkCount := uint64(0)
	reader := bufio.NewReaderSize(p.spool, 64*1024)
	var storage [otlpRecordBytes]byte
	writeIndex := func() error {
		chunkCount++
		b := chunk.bytes()
		_, err := indexOut.Write(b[:])
		return err
	}
	for offset := uint64(0); offset < bytes; offset += otlpRecordBytes {
		record, err := readOTLPRecord(reader, &storage)
		if err != nil {
			return err
		}
		if record[1] == math.MaxUint64 {
			if hasChunk {
				chunk.dataEnd = offset
				if err = writeIndex(); err != nil {
					return err
				}
			}
			if record[2] < record[0] {
				return errors.New("invalid OTLP chunk time range")
			}
			period := record[3]
			if period == 0 {
				period = otlpDefaultPeriod(p)
			}
			chunk = otlpChunk{start: record[0], end: record[2], dataStart: offset + otlpRecordBytes, dataEnd: bytes, period: period}
			hasChunk = true
		} else {
			if !hasChunk {
				return errors.New("OTLP sample precedes chunk header")
			}
			if record[1] >= uint64(p.nodes.n) || record[4] >= uint64(p.strings.refs.n) {
				return errors.New("invalid OTLP sample spool reference")
			}
			chunk.writeValues = chunk.writeValues || record[2] != 1
			dictionary.addEvent(p, record)
		}
	}
	if hasChunk {
		if err = writeIndex(); err != nil {
			return err
		}
	}
	if err = indexOut.Flush(); err != nil {
		return err
	}
	dictionary.finish(p)
	encoded, err := p.temp()
	if err != nil {
		return err
	}
	defer encoded.Close()
	counter := countedWriter{writer: encoded}
	encodedOut := bufio.NewWriterSize(&counter, 128*1024)
	q := pbOut{out: encodedOut}
	scopeSize := uint64(0)
	rawReader := bufio.NewReaderSize(p.spool, 64*1024)
	for i := uint64(0); i < chunkCount; i++ {
		chunk, err = readOTLPChunk(index, i)
		if err != nil {
			return err
		}
		before := counter.n + uint64(encodedOut.Buffered())
		if err = writeOTLPChunk(p, chunk, rawReader, &dictionary, &q); err != nil {
			return err
		}
		chunk.encodedSize = counter.n + uint64(encodedOut.Buffered()) - before
		b := chunk.bytes()
		if _, err = index.WriteAt(b[:], int64(i*otlpIndexBytes)); err != nil {
			return err
		}
		part := pbMSize(2, chunk.encodedSize)
		if part > math.MaxUint64-scopeSize {
			return errors.New("OTLP profile size overflow")
		}
		scopeSize += part
	}
	dictionaryStart := counter.n + uint64(encodedOut.Buffered())
	writeOTLPDictionary(p, &q, &dictionary)
	if q.err != nil {
		return q.err
	}
	dictionarySize := counter.n + uint64(encodedOut.Buffered()) - dictionaryStart
	if err = encodedOut.Flush(); err != nil {
		return err
	}
	if _, err = encoded.Seek(0, io.SeekStart); err != nil {
		return err
	}
	q = pbOut{out: out}
	q.h(1, pbMSize(2, scopeSize))
	q.h(2, scopeSize)
	encodedIn := bufio.NewReaderSize(encoded, 128*1024)
	for i := uint64(0); i < chunkCount; i++ {
		chunk, err = readOTLPChunk(index, i)
		if err != nil {
			return err
		}
		q.h(2, chunk.encodedSize)
		if q.err != nil {
			return q.err
		}
		if chunk.encodedSize > math.MaxInt64 {
			return errors.New("OTLP chunk too large")
		}
		if _, err = io.CopyN(out, encodedIn, int64(chunk.encodedSize)); err != nil {
			return err
		}
	}
	q.h(2, dictionarySize)
	if q.err != nil {
		return q.err
	}
	if dictionarySize > math.MaxInt64 {
		return errors.New("OTLP dictionary too large")
	}
	_, err = io.CopyN(out, encodedIn, int64(dictionarySize))
	return err
}
