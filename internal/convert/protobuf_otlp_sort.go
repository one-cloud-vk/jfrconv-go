// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
)

const otlpMergeFanIn = 8
const otlpMergeLevels = 9

// Group key occupies nativeRecord.Time; Sequence preserves event order.
// Only equal-level runs merge. Each event is rewritten at most once per level,
// instead of repeatedly merging a growing prefix. Nine base-eight levels cap
// run metadata/final merge inputs at 63; the production bound supports almost
// 4.4 trillion events in one chunk (roughly176TB of raw records).
type otlpSorter struct {
	nativeSorter
	levels        []uint8
	runRecords    []uint64
	mergedRecords uint64
	maxLevels     int // zero uses the production bound; tests force its boundary.
}

func (s *otlpSorter) push(r nativeRecord) error {
	limit := s.runItems
	if limit == 0 {
		limit = nativeRunItems
	}
	if s.sequence == math.MaxUint64 || len(s.runs) >= mergeFanIn && len(s.buf)+1 >= limit {
		return errors.New("OTLP event stream exceeds bounded external sort limit")
	}
	if s.runs == nil && len(s.buf)+1 >= limit {
		s.runs = make([]string, 0, mergeFanIn)
		s.levels = make([]uint8, 0, mergeFanIn)
		s.runRecords = make([]uint64, 0, mergeFanIn)
	}
	before := len(s.runs)
	count := len(s.buf) + 1
	if err := s.nativeSorter.push(r); err != nil {
		return err
	}
	if len(s.runs) == before {
		return nil
	}
	s.levels = append(s.levels, 0)
	s.runRecords = append(s.runRecords, uint64(count))
	return s.compact()
}
func (s *otlpSorter) compact() error {
	levels := s.maxLevels
	if levels == 0 {
		levels = otlpMergeLevels
	}
	for level := 0; ; level++ {
		var paths [otlpMergeFanIn]string
		count := 0
		records := uint64(0)
		for i, path := range s.runs {
			if int(s.levels[i]) == level {
				paths[count] = path
				records += s.runRecords[i]
				count++
			}
		}
		if count < otlpMergeFanIn {
			return nil
		}
		if level+1 >= levels {
			return errors.New("OTLP event stream exceeds bounded external sort limit")
		}
		f, err := os.CreateTemp(s.dir, "jfrconv-go-otlp-merge-*")
		if err != nil {
			return err
		}
		name := f.Name()
		w := bufio.NewWriterSize(f, 128*1024)
		err = mergeNative(paths[:count], func(r nativeRecord) error { return writeNative(w, r) })
		if err == nil {
			err = w.Flush()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(name)
			return err
		}
		s.mergedRecords += records
		kept := 0
		for i, path := range s.runs {
			if int(s.levels[i]) == level {
				os.Remove(path)
			} else {
				s.runs[kept], s.levels[kept], s.runRecords[kept] = path, s.levels[i], s.runRecords[i]
				kept++
			}
		}
		s.runs = append(s.runs[:kept], name)
		s.levels = append(s.levels[:kept], uint8(level+1))
		s.runRecords = append(s.runRecords[:kept], records)
	}
}
func (s *otlpSorter) each(visit func(nativeRecord) error) error {
	if len(s.runs) == 0 {
		return s.nativeSorter.each(visit)
	}
	if len(s.buf) != 0 {
		if len(s.runs) >= mergeFanIn {
			return errors.New("OTLP event stream exceeds bounded external sort limit")
		}
		count := len(s.buf)
		if err := s.nativeSorter.flush(); err != nil {
			return err
		}
		s.levels = append(s.levels, 0)
		s.runRecords = append(s.runRecords, uint64(count))
		if err := s.compact(); err != nil {
			return err
		}
	}
	s.buf = nil
	return mergeNative(s.runs, visit)
}
func (s *otlpSorter) close() {
	s.nativeSorter.close()
	s.levels, s.runRecords = nil, nil
}

// These files live only for one chunk, so their names are not accumulated in
// profile.scratch (which would otherwise grow with the number of chunks).
func otlpTemp(dir string) (*os.File, func(), error) {
	f, err := os.CreateTemp(dir, "jfrconv-go-otlp-*")
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { f.Close(); os.Remove(f.Name()) }, nil
}

type otlpGroupSpan struct{ key, leaf, thread, offset, count, valuesSize uint64 }

func (g otlpGroupSpan) write(w *bufio.Writer) error {
	var b [48]byte
	for i, n := range [6]uint64{g.key, g.leaf, g.thread, g.offset, g.count, g.valuesSize} {
		binary.LittleEndian.PutUint64(b[i*8:], n)
	}
	_, err := w.Write(b[:])
	return err
}
func readOTLPGroup(r io.Reader) (otlpGroupSpan, error) {
	var b [48]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return otlpGroupSpan{}, err
	}
	return otlpGroupSpan{
		key: binary.LittleEndian.Uint64(b[:]), leaf: binary.LittleEndian.Uint64(b[8:]),
		thread: binary.LittleEndian.Uint64(b[16:]), offset: binary.LittleEndian.Uint64(b[24:]),
		count: binary.LittleEndian.Uint64(b[32:]), valuesSize: binary.LittleEndian.Uint64(b[40:]),
	}, nil
}

func writeOTLPChunk(p *profile, chunk otlpChunk, rawReader *bufio.Reader, d *otlpDictionary, q *pbOut) error {
	base := uint64(p.strings.refs.n)
	unit := base + 1 // count
	if p.options.Total {
		unit = base + 2
	}
	for _, f := range []struct{ tag, unit uint64 }{{1, unit}, {5, base + 2}} {
		q.h(f.tag, pbUSize(1, base)+pbUSize(2, f.unit))
		q.u(1, base)
		q.u(2, f.unit)
	}
	q.u(6, chunk.period)
	q.v(3<<3 | 1)
	q.fixed64(chunk.start)
	q.u(4, difference(chunk.end, chunk.start))
	if q.err != nil || chunk.dataStart == chunk.dataEnd {
		return q.err
	}
	var sorter otlpSorter
	sorter.dir = p.options.TempDir
	defer sorter.close()
	err := eachOTLPRecord(p.spool, chunk, rawReader, func(r otlpRecord) error {
		return sorter.push(nativeRecord{Time: r[3], Address: r[0], Size: r[2], TID: r[1], StackID: r[4]})
	})
	if err != nil {
		return err
	}
	data, closeData, err := otlpTemp(p.options.TempDir)
	if err != nil {
		return err
	}
	defer closeData()
	groups, closeGroups, err := otlpTemp(p.options.TempDir)
	if err != nil {
		return err
	}
	defer closeGroups()
	dataOut := bufio.NewWriterSize(data, 128*1024)
	groupOut := bufio.NewWriterSize(groups, 64*1024)
	var group otlpGroupSpan
	offset := uint64(0)
	err = sorter.each(func(r nativeRecord) error {
		if group.count == 0 || group.key != r.Time {
			if group.count != 0 {
				if err := group.write(groupOut); err != nil {
					return err
				}
			}
			group = otlpGroupSpan{key: r.Time, leaf: r.TID, thread: r.StackID, offset: offset}
		} else if group.leaf != r.TID || group.thread != r.StackID {
			return errors.New("inconsistent OTLP stack/thread group")
		}
		if group.count == math.MaxUint64/16 || group.valuesSize > math.MaxUint64-pbVSize(r.Size) {
			return errors.New("OTLP event group too large")
		}
		group.count++
		group.valuesSize += pbVSize(r.Size)
		if dataOut.Available() < 16 {
			if err := dataOut.Flush(); err != nil {
				return err
			}
		}
		b := binary.LittleEndian.AppendUint64(dataOut.AvailableBuffer(), r.Address)
		b = binary.LittleEndian.AppendUint64(b, r.Size)
		if _, err := dataOut.Write(b); err != nil {
			return err
		}
		offset += 16
		return nil
	})
	if err != nil {
		return err
	}
	if group.count != 0 {
		if err = group.write(groupOut); err != nil {
			return err
		}
	}
	if err = dataOut.Flush(); err != nil {
		return err
	}
	if err = groupOut.Flush(); err != nil {
		return err
	}
	if _, err = groups.Seek(0, io.SeekStart); err != nil {
		return err
	}
	groupIn := bufio.NewReaderSize(groups, 64*1024)
	dataIn := bufio.NewReaderSize(data, 128*1024)
	for {
		group, err = readOTLPGroup(groupIn)
		if err == io.EOF {
			return q.err
		}
		if err != nil {
			return err
		}
		stack, thread := uint64(d.stacks[group.leaf]), uint64(d.attributeIDs[uint32(group.thread)])
		size := pbUSize(1, stack) + pbUSize(2, thread) + pbMSize(5, group.count*8)
		if chunk.writeValues {
			size += pbMSize(4, group.valuesSize)
		}
		q.h(2, size)
		q.u(1, stack)
		q.u(2, thread)
		q.h(5, group.count*8)
		if err = writeOTLPColumn(data, dataIn, group, false, q); err != nil {
			return err
		}
		if chunk.writeValues {
			q.h(4, group.valuesSize)
			if err = writeOTLPColumn(data, dataIn, group, true, q); err != nil {
				return err
			}
		}
	}
}
func writeOTLPColumn(file *os.File, reader *bufio.Reader, group otlpGroupSpan, values bool, q *pbOut) error {
	if group.offset > math.MaxInt64 {
		return errors.New("OTLP group offset exceeds file limit")
	}
	if _, err := file.Seek(int64(group.offset), io.SeekStart); err != nil {
		return err
	}
	reader.Reset(file)
	var b [16]byte
	for i := uint64(0); i < group.count; i++ {
		if _, err := io.ReadFull(reader, b[:]); err != nil {
			return err
		}
		if values {
			q.v(binary.LittleEndian.Uint64(b[8:]))
		} else {
			q.raw(b[:8])
		}
		if q.err != nil {
			return q.err
		}
	}
	return nil
}
