// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
// Protobuf schemas follow the converter bundled with async-profiler v4.1.
package convert

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
)

func init() {
	writers["pprof"] = writePprof
	writers["pb.gz"] = writePprofGzip
	writers["otlp"] = writeOTLP
}

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
	for _, s := range []string{p.ValueType, p.Unit, p.TotalUnit, "Produced by jfrconv-go (async-profiler 4.1 compatible)", "jfrconv-go", "thread", "category"} {
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

type otlpAttribute struct {
	role uint8
	name uint32
}
type otlpChunk struct{ start, end, dataStart, dataEnd, encodedSize uint64 }
type otlpRecord [5]uint64

const otlpRecordBytes = 40

func readOTLPRecord(reader io.Reader, storage *[otlpRecordBytes]byte) (otlpRecord, error) {
	if _, err := io.ReadFull(reader, storage[:]); err != nil {
		return otlpRecord{}, err
	}
	var result otlpRecord
	for i := range result {
		result[i] = binary.LittleEndian.Uint64(storage[i*8 : i*8+8])
	}
	return result, nil
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
func compactOTLPLocations(p *profile) []uint32 {
	locations := make([]uint32, p.frames.n)
	for i := range locations {
		locations[i] = math.MaxUint32
	}
	locations[0] = 0
	for id := uint32(1); id < p.nodes.n; id++ {
		f := p.nodes.at(id).Frame
		if p.frames.at(f).Role == 0 {
			locations[f] = 0
		}
	}
	next := uint32(1)
	for i := 1; i < len(locations); i++ {
		if locations[i] != math.MaxUint32 {
			locations[i] = next
			next++
		}
	}
	return locations
}
func writeOTLPDictionary(p *profile, q *pbOut, attributes []otlpAttribute, locations []uint32) {
	size := pbMSize(1, 0)
	for name := uint32(0); name < p.strings.refs.n; name++ {
		size += pbMSize(3, pbUSize(1, uint64(name)))
	}
	for id := uint32(0); id < p.frames.n; id++ {
		if locations[id] != math.MaxUint32 {
			f := p.frames.at(id)
			line := pbUSize(1, uint64(f.Name)) + pbUSize(2, uint64(max(f.Line, 0)))
			size += pbMSize(2, pbUSize(1, 0)+pbMSize(3, line))
		}
	}
	for id := uint32(0); id < p.strings.refs.n; id++ {
		size += pbMSize(5, uint64(len(p.strings.text(id))))
	}
	for _, s := range []string{p.ValueType, "count", p.TotalUnit} {
		size += pbMSize(5, uint64(len(s)))
	}
	for _, attribute := range attributes {
		key := ""
		switch attribute.role {
		case 1:
			key = "thread.name"
		case 2:
			key = "category"
		}
		size += pbMSize(6, pbMSize(1, uint64(len(key)))+pbMSize(2, pbMSize(1, uint64(len(p.strings.text(attribute.name))))))
	}
	q.h(2, size)
	q.h(1, 0)
	for name := uint32(0); name < p.strings.refs.n; name++ {
		q.h(3, pbUSize(1, uint64(name)))
		q.u(1, uint64(name))
	}
	for id := uint32(0); id < p.frames.n; id++ {
		if locations[id] == math.MaxUint32 {
			continue
		}
		f := p.frames.at(id)
		line := uint64(max(f.Line, 0))
		lineSize := pbUSize(1, uint64(f.Name)) + pbUSize(2, line)
		q.h(2, pbUSize(1, 0)+pbMSize(3, lineSize))
		q.u(1, 0)
		q.h(3, lineSize)
		q.u(1, uint64(f.Name))
		q.u(2, line)
	}
	for id := uint32(0); id < p.strings.refs.n; id++ {
		q.s(5, p.strings.text(id))
	}
	for _, s := range []string{p.ValueType, "count", p.TotalUnit} {
		q.s(5, s)
	}
	for _, attribute := range attributes {
		key := ""
		switch attribute.role {
		case 1:
			key = "thread.name"
		case 2:
			key = "category"
		}
		value := p.strings.text(attribute.name)
		q.h(6, pbMSize(1, uint64(len(key)))+pbMSize(2, pbMSize(1, uint64(len(value)))))
		q.s(1, key)
		q.h(2, pbMSize(1, uint64(len(value))))
		q.s(1, value)
	}
}
func writeOTLPChunk(p *profile, raw *os.File, chunk otlpChunk, rawReader *bufio.Reader, attributes map[otlpAttribute]uint64, locations []uint32, q *pbOut) error {
	base := uint64(p.strings.refs.n)
	for _, unit := range []uint64{base + 1, base + 2} {
		q.h(1, pbUSize(1, base)+pbUSize(2, unit)+pbUSize(3, 2))
		q.u(1, base)
		q.u(2, unit)
		q.u(3, 2)
	}
	q.u(4, chunk.start)
	q.u(5, difference(chunk.end, chunk.start))
	nextLocation, packedSize := uint64(0), uint64(0)
	err := eachOTLPRecord(raw, chunk, rawReader, func(record otlpRecord) error {
		length, categorySize := uint64(0), uint64(0)
		for at := uint32(record[1]); at != 0; at = p.nodes.at(at).Parent {
			fID := p.nodes.at(at).Frame
			f := p.frames.at(fID)
			if f.Role == 0 {
				length++
				packedSize += pbVSize(uint64(locations[fID]))
			} else if f.Role == 2 {
				categorySize += pbUSize(4, attributes[otlpAttribute{2, f.Name}])
			}
		}
		thread := attributes[otlpAttribute{1, uint32(record[4])}]
		valuesSize := pbVSize(record[2]) + pbVSize(record[3])
		sampleSize := pbUSize(1, nextLocation) + pbUSize(2, length) + pbUSize(6, record[0]) + pbUSize(4, thread) + categorySize + pbMSize(3, valuesSize)
		q.h(2, sampleSize)
		q.u(1, nextLocation)
		q.u(2, length)
		q.u(6, record[0])
		q.u(4, thread)
		for at := uint32(record[1]); at != 0; at = p.nodes.at(at).Parent {
			f := p.frames.at(p.nodes.at(at).Frame)
			if f.Role == 2 {
				q.u(4, attributes[otlpAttribute{2, f.Name}])
			}
		}
		q.h(3, valuesSize)
		q.v(record[2])
		q.v(record[3])
		nextLocation += length
		return q.err
	})
	if err != nil {
		return err
	}
	q.h(3, packedSize)
	return eachOTLPRecord(raw, chunk, rawReader, func(record otlpRecord) error {
		for at := uint32(record[1]); at != 0; at = p.nodes.at(at).Parent {
			fID := p.nodes.at(at).Frame
			if p.frames.at(fID).Role == 0 {
				q.v(uint64(locations[fID]))
			}
		}
		return q.err
	})
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

// writeOTLP stores chunk indexes and encoded payloads on disk. Its memory does
// not grow with event count or chunk count; each chunk is encoded only once.
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
	attributes := []otlpAttribute{{}}
	ids := make(map[otlpAttribute]uint64)
	addAttribute := func(a otlpAttribute) {
		if _, ok := ids[a]; !ok {
			ids[a] = uint64(len(attributes))
			attributes = append(attributes, a)
		}
	}
	for id := uint32(0); id < p.frames.n; id++ {
		f := p.frames.at(id)
		if f.Role == 2 {
			addAttribute(otlpAttribute{2, f.Name})
		}
	}
	var chunk otlpChunk
	hasChunk := false
	chunkCount := uint64(0)
	reader := bufio.NewReaderSize(p.spool, 64*1024)
	var storage [otlpRecordBytes]byte
	writeIndex := func() error {
		chunkCount++
		return writeOTLPRecord(indexOut, otlpRecord{chunk.start, chunk.end, chunk.dataStart, chunk.dataEnd, 0})
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
			chunk = otlpChunk{start: record[0], end: record[2], dataStart: offset + otlpRecordBytes, dataEnd: bytes}
			hasChunk = true
		} else {
			if !hasChunk {
				return errors.New("OTLP sample precedes chunk header")
			}
			if record[1] >= uint64(p.nodes.n) || record[4] >= uint64(p.strings.refs.n) {
				return errors.New("invalid OTLP sample spool reference")
			}
			addAttribute(otlpAttribute{1, uint32(record[4])})
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
	encoded, err := p.temp()
	if err != nil {
		return err
	}
	defer encoded.Close()
	counter := countedWriter{writer: encoded}
	encodedOut := bufio.NewWriterSize(&counter, 128*1024)
	q := pbOut{out: encodedOut}
	locations := compactOTLPLocations(p)
	scopeSize := uint64(0)
	rawReader := bufio.NewReaderSize(p.spool, 64*1024)
	for i := uint64(0); i < chunkCount; i++ {
		if _, err = index.ReadAt(storage[:], int64(i*otlpRecordBytes)); err != nil {
			return err
		}
		record := decodeOTLPRecord(&storage)
		chunk = otlpChunk{start: record[0], end: record[1], dataStart: record[2], dataEnd: record[3]}
		before := counter.n + uint64(encodedOut.Buffered())
		if err = writeOTLPChunk(p, p.spool, chunk, rawReader, ids, locations, &q); err != nil {
			return err
		}
		length := counter.n + uint64(encodedOut.Buffered()) - before
		var lengthBytes [8]byte
		binary.LittleEndian.PutUint64(lengthBytes[:], length)
		if _, err = index.WriteAt(lengthBytes[:], int64(i*otlpRecordBytes+32)); err != nil {
			return err
		}
		part := pbMSize(2, length)
		if part > math.MaxUint64-scopeSize {
			return errors.New("OTLP profile size overflow")
		}
		scopeSize += part
	}
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
		if _, err = index.ReadAt(storage[:], int64(i*otlpRecordBytes)); err != nil {
			return err
		}
		length := binary.LittleEndian.Uint64(storage[32:40])
		q.h(2, length)
		if q.err != nil {
			return q.err
		}
		if length > math.MaxInt64 {
			return errors.New("OTLP chunk too large")
		}
		if _, err = io.CopyN(out, encodedIn, int64(length)); err != nil {
			return err
		}
	}
	writeOTLPDictionary(p, &q, attributes, locations)
	return q.err
}
func decodeOTLPRecord(storage *[otlpRecordBytes]byte) (record otlpRecord) {
	for i := range record {
		record[i] = binary.LittleEndian.Uint64(storage[i*8 : i*8+8])
	}
	return record
}
