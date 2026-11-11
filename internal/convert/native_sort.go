// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"sort"
)

const nativeRunItems = 32768
const mergeFanIn = 63

type nativeRecord struct{ Time, Address, Size, TID, StackID, Sequence uint64 }
type nativeSorter struct {
	dir      string
	buf      []nativeRecord
	runs     []string
	sequence uint64
	runItems int // zero uses the production bound; small values exercise merges.
}

func (s *nativeSorter) push(r nativeRecord) error {
	r.Sequence = s.sequence
	s.sequence++
	if s.buf == nil {
		s.buf = make([]nativeRecord, 0, 512)
	}
	s.buf = append(s.buf, r)
	limit := s.runItems
	if limit == 0 {
		limit = nativeRunItems
	}
	if len(s.buf) >= limit {
		return s.flush()
	}
	return nil
}
func lessNative(a, b nativeRecord) bool {
	if a.Time == b.Time {
		return a.Sequence < b.Sequence
	}
	return a.Time < b.Time
}
func writeNative(w *bufio.Writer, r nativeRecord) error {
	if w.Available() < 48 {
		if err := w.Flush(); err != nil {
			return err
		}
	}
	b := w.AvailableBuffer()
	for _, v := range [6]uint64{r.Time, r.Address, r.Size, r.TID, r.StackID, r.Sequence} {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	_, err := w.Write(b)
	return err
}
func (s *nativeSorter) flush() error {
	if len(s.buf) == 0 {
		return nil
	}
	sort.Slice(s.buf, func(i, j int) bool { return lessNative(s.buf[i], s.buf[j]) })
	f, err := os.CreateTemp(s.dir, "jfrconv-go-native-*")
	if err != nil {
		return err
	}
	name := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	w := bufio.NewWriterSize(f, 128*1024)
	for _, record := range s.buf {
		if err := writeNative(w, record); err != nil {
			return err
		}
	}
	if err = w.Flush(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	s.runs = append(s.runs, name)
	s.buf = s.buf[:0]
	return nil
}

type nativeInput struct {
	file   *os.File
	reader *bufio.Reader
	bytes  [48]byte
}

func (i *nativeInput) next() (nativeRecord, error) {
	_, err := io.ReadFull(i.reader, i.bytes[:])
	if err != nil {
		return nativeRecord{}, err
	}
	b := i.bytes[:]
	return nativeRecord{binary.LittleEndian.Uint64(b), binary.LittleEndian.Uint64(b[8:]), binary.LittleEndian.Uint64(b[16:]), binary.LittleEndian.Uint64(b[24:]), binary.LittleEndian.Uint64(b[32:]), binary.LittleEndian.Uint64(b[40:])}, nil
}

type nativeHead struct {
	record nativeRecord
	run    int
}

func heapNativeLess(a, b nativeHead) bool {
	if a.record == b.record {
		return a.run < b.run
	}
	return lessNative(a.record, b.record)
}
func pushNative(heap []nativeHead, h nativeHead) []nativeHead {
	heap = append(heap, h)
	i := len(heap) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if !heapNativeLess(heap[i], heap[parent]) {
			break
		}
		heap[i], heap[parent] = heap[parent], heap[i]
		i = parent
	}
	return heap
}
func popNative(heap []nativeHead) (nativeHead, []nativeHead) {
	top := heap[0]
	heap[0] = heap[len(heap)-1]
	heap = heap[:len(heap)-1]
	for i := 0; ; {
		child := i*2 + 1
		if child >= len(heap) {
			break
		}
		if child+1 < len(heap) && heapNativeLess(heap[child+1], heap[child]) {
			child++
		}
		if !heapNativeLess(heap[child], heap[i]) {
			break
		}
		heap[child], heap[i] = heap[i], heap[child]
		i = child
	}
	return top, heap
}
func mergeNative(paths []string, visit func(nativeRecord) error) error {
	inputs := make([]nativeInput, len(paths))
	defer func() {
		for i := range inputs {
			if inputs[i].file != nil {
				inputs[i].file.Close()
			}
		}
	}()
	heap := make([]nativeHead, 0, len(paths))
	for i, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		inputs[i] = nativeInput{file: f, reader: bufio.NewReaderSize(f, 16*1024)}
		r, err := inputs[i].next()
		if err == io.EOF {
			continue
		}
		if err != nil {
			return err
		}
		heap = pushNative(heap, nativeHead{r, i})
	}
	for len(heap) > 0 {
		var h nativeHead
		h, heap = popNative(heap)
		if err := visit(h.record); err != nil {
			return err
		}
		r, err := inputs[h.run].next()
		if err == io.EOF {
			continue
		}
		if err != nil {
			return err
		}
		heap = pushNative(heap, nativeHead{r, h.run})
	}
	return nil
}
func (s *nativeSorter) each(visit func(nativeRecord) error) error {
	if len(s.runs) == 0 {
		sort.Slice(s.buf, func(i, j int) bool { return lessNative(s.buf[i], s.buf[j]) })
		for _, r := range s.buf {
			if err := visit(r); err != nil {
				return err
			}
		}
		return nil
	}
	if err := s.flush(); err != nil {
		return err
	}
	s.buf = nil
	for len(s.runs) > mergeFanIn {
		old := s.runs
		s.runs = nil
		for from := 0; from < len(old); from += mergeFanIn {
			to := from + mergeFanIn
			if to > len(old) {
				to = len(old)
			}
			group := old[from:to]
			if len(group) == 1 {
				s.runs = append(s.runs, group[0])
				continue
			}
			f, err := os.CreateTemp(s.dir, "jfrconv-go-native-merge-*")
			if err != nil {
				s.runs = append(s.runs, old[from:]...)
				return err
			}
			w := bufio.NewWriterSize(f, 128*1024)
			err = mergeNative(group, func(r nativeRecord) error { return writeNative(w, r) })
			if err == nil {
				err = w.Flush()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				os.Remove(f.Name())
				s.runs = append(s.runs, old[from:]...)
				return err
			}
			s.runs = append(s.runs, f.Name())
			for _, name := range group {
				os.Remove(name)
			}
		}
	}
	return mergeNative(s.runs, visit)
}
func (s *nativeSorter) close() {
	if s == nil {
		return
	}
	for _, name := range s.runs {
		os.Remove(name)
	}
	s.runs = nil
	s.buf = nil
}
