// Copyright 2026 jfrconv-rs and jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
)

const heatLimit uint64 = 1<<30 - 1
const heatRunEntries = 65536
const heatFanIn = 8

// Unlinked scratch files require no growing list of historical run names and
// are reclaimed by the kernel even if the supervised worker is killed.
func heatTemp(dir string) (*os.File, error) {
	f, e := os.CreateTemp(dir, "jfrconv-heat-*")
	if e != nil {
		return nil, e
	}
	if e = os.Remove(f.Name()); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}

type heatRecord struct{ Key, Count uint64 }

func heatRawRead(r io.Reader) (heatRecord, bool, error) {
	var b [16]byte
	if _, e := io.ReadFull(r, b[:1]); e == io.EOF {
		return heatRecord{}, false, nil
	} else if e != nil {
		return heatRecord{}, false, e
	}
	if _, e := io.ReadFull(r, b[1:]); e != nil {
		return heatRecord{}, false, e
	}
	return heatRecord{binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint64(b[8:])}, true, nil
}
func heatRead(r io.Reader) (heatRecord, bool, error) {
	v, ok, e := heatRawRead(r)
	if e == nil && ok && (v.Count == 0 || v.Count > heatLimit) {
		return v, false, fmt.Errorf("invalid heatmap multiplicity")
	}
	return v, ok, e
}
func heatPut(w *bufio.Writer, v heatRecord) error {
	if w.Available() < 16 {
		if e := w.Flush(); e != nil {
			return e
		}
	}
	b := w.AvailableBuffer()
	b = binary.LittleEndian.AppendUint64(b, v.Key)
	b = binary.LittleEndian.AppendUint64(b, v.Count)
	_, e := w.Write(b)
	return e
}
func heatAdd(a, b uint64) (uint64, error) {
	if b > heatLimit-a {
		return 0, fmt.Errorf("heatmap exceeds the original 30-bit format limit")
	}
	return a + b, nil
}

type heatHeapItem struct {
	heatRecord
	Input int
}
type heatHeap []heatHeapItem

func (h *heatHeap) push(v heatHeapItem) {
	*h = append(*h, v)
	i := len(*h) - 1
	for i > 0 {
		p := (i - 1) / 2
		if (*h)[p].Key <= v.Key {
			break
		}
		(*h)[i] = (*h)[p]
		i = p
	}
	(*h)[i] = v
}
func (h *heatHeap) pop() heatHeapItem {
	v := (*h)[0]
	last := (*h)[len(*h)-1]
	*h = (*h)[:len(*h)-1]
	if len(*h) > 0 {
		i := 0
		for {
			j := i*2 + 1
			if j >= len(*h) {
				break
			}
			if j+1 < len(*h) && (*h)[j+1].Key < (*h)[j].Key {
				j++
			}
			if last.Key <= (*h)[j].Key {
				break
			}
			(*h)[i] = (*h)[j]
			i = j
		}
		(*h)[i] = last
	}
	return v
}

func heatMerge(files []*os.File, dir string) (out *os.File, err error) {
	out, err = heatTemp(dir)
	if err != nil {
		return nil, err
	}
	owned := out
	defer func() {
		if err != nil {
			owned.Close()
		}
	}()
	readers := make([]*bufio.Reader, len(files))
	var heap heatHeap
	for i, f := range files {
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		readers[i] = bufio.NewReaderSize(f, 16*1024)
		v, ok, e := heatRead(readers[i])
		if e != nil {
			return nil, e
		}
		if ok {
			heap.push(heatHeapItem{v, i})
		}
	}
	w := bufio.NewWriterSize(out, 64*1024)
	var previous heatRecord
	have := false
	for len(heap) > 0 {
		item := heap.pop()
		if have && previous.Key == item.Key {
			previous.Count, err = heatAdd(previous.Count, item.Count)
			if err != nil {
				return nil, err
			}
		} else {
			if have {
				if err = heatPut(w, previous); err != nil {
					return nil, err
				}
			}
			previous = item.heatRecord
			have = true
		}
		v, ok, e := heatRead(readers[item.Input])
		if e != nil {
			return nil, e
		}
		if ok {
			heap.push(heatHeapItem{v, item.Input})
		}
	}
	if have {
		if err = heatPut(w, previous); err != nil {
			return nil, err
		}
	}
	if err = w.Flush(); err != nil {
		return nil, err
	}
	_, err = out.Seek(0, io.SeekStart)
	return out, err
}

type heatRun struct {
	File  *os.File
	Level int
}
type heatSorter struct {
	keys  []heatRecord
	runs  []heatRun
	total uint64
	dir   string
}

func (s *heatSorter) close() {
	for _, r := range s.runs {
		r.File.Close()
	}
	s.runs = nil
}
func (s *heatSorter) push(key, count uint64) error {
	if count == 0 {
		return fmt.Errorf("zero heatmap multiplicity")
	}
	var e error
	s.total, e = heatAdd(s.total, count)
	if e != nil {
		return e
	}
	s.keys = append(s.keys, heatRecord{key, count})
	if len(s.keys) == heatRunEntries {
		return s.flush()
	}
	return nil
}
func (s *heatSorter) flush() error {
	if len(s.keys) == 0 {
		return nil
	}
	sort.Slice(s.keys, func(i, j int) bool { return s.keys[i].Key < s.keys[j].Key })
	f, e := heatTemp(s.dir)
	if e != nil {
		return e
	}
	w := bufio.NewWriterSize(f, 64*1024)
	previous := s.keys[0]
	for _, v := range s.keys[1:] {
		if v.Key == previous.Key {
			previous.Count, e = heatAdd(previous.Count, v.Count)
			if e != nil {
				f.Close()
				return e
			}
		} else {
			if e = heatPut(w, previous); e != nil {
				f.Close()
				return e
			}
			previous = v
		}
	}
	if e = heatPut(w, previous); e == nil {
		e = w.Flush()
	}
	if e != nil {
		f.Close()
		return e
	}
	s.keys = s.keys[:0]
	s.runs = append(s.runs, heatRun{f, 0})
	// Tiered compaction bounds open descriptors to <8*5 under the format's
	// sample limit and avoids repeatedly rewriting one ever-growing large run.
	for level := 0; ; level++ {
		var files []*os.File
		for _, r := range s.runs {
			if r.Level == level {
				files = append(files, r.File)
			}
		}
		if len(files) < heatFanIn {
			break
		}
		merged, e := heatMerge(files, s.dir)
		if e != nil {
			return e
		}
		kept := s.runs[:0]
		for _, r := range s.runs {
			if r.Level == level {
				r.File.Close()
			} else {
				kept = append(kept, r)
			}
		}
		s.runs = append(kept, heatRun{merged, level + 1})
	}
	return nil
}
func (s *heatSorter) finish() (*os.File, error) {
	if e := s.flush(); e != nil {
		return nil, e
	}
	s.keys = nil
	if len(s.runs) == 0 {
		return heatTemp(s.dir)
	}
	for len(s.runs) > heatFanIn {
		var files []*os.File
		for _, r := range s.runs[:heatFanIn] {
			files = append(files, r.File)
		}
		merged, e := heatMerge(files, s.dir)
		if e != nil {
			return nil, e
		}
		for _, f := range files {
			f.Close()
		}
		s.runs = append(s.runs[heatFanIn:], heatRun{merged, 0})
	}
	var files []*os.File
	for _, r := range s.runs {
		files = append(files, r.File)
	}
	out, e := heatMerge(files, s.dir)
	s.close()
	return out, e
}
