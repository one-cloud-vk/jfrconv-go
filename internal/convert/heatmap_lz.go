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

const heatLZEntries = 262144
const heatLZRuns = 8
const heatLZPages = 64

type heatLZRun struct {
	File       *os.File
	ID, Length uint64
}
type heatPageKey struct{ Run, Page uint64 }
type heatLZPage struct {
	Key  heatPageKey
	Data [4096]byte
}
type heatLZ struct {
	cache                           map[uint64]uint32
	runs                            []heatLZRun
	pages                           []heatLZPage
	pageSlots                       map[heatPageKey]int
	pageNext                        int
	next                            uint32
	runID                           uint64
	dir                             string
	entryLimit, runLimit, pageLimit int
}

func newHeatLZ(dir string) *heatLZ {
	return &heatLZ{cache: make(map[uint64]uint32), pageSlots: make(map[heatPageKey]int), next: 1, dir: dir, entryLimit: heatLZEntries, runLimit: heatLZRuns, pageLimit: heatLZPages}
}
func (l *heatLZ) close() {
	for _, r := range l.runs {
		r.File.Close()
	}
	l.runs = nil
}
func (l *heatLZ) record(run heatLZRun, index uint64) (uint64, uint32, error) {
	offset := index * 16
	key := heatPageKey{run.ID, offset / 4096}
	slot, ok := l.pageSlots[key]
	if !ok {
		if len(l.pages) < l.pageLimit {
			slot = len(l.pages)
			l.pages = append(l.pages, heatLZPage{Key: key})
		} else {
			slot = l.pageNext
			l.pageNext = (l.pageNext + 1) % l.pageLimit
			delete(l.pageSlots, l.pages[slot].Key)
			l.pages[slot].Key = key
		}
		count := run.Length*16 - key.Page*4096
		if count > 4096 {
			count = 4096
		}
		n, e := run.File.ReadAt(l.pages[slot].Data[:count], int64(key.Page*4096))
		if e != nil && e != io.EOF {
			return 0, 0, e
		}
		if uint64(n) != count {
			return 0, 0, io.ErrUnexpectedEOF
		}
		l.pageSlots[key] = slot
	}
	b := l.pages[slot].Data[offset%4096:]
	return binary.LittleEndian.Uint64(b[:8]), binary.LittleEndian.Uint32(b[8:12]), nil
}
func (l *heatLZ) append(parent, method uint32) (uint32, error) {
	if method == 0 {
		return 0, fmt.Errorf("zero LZ method")
	}
	key := uint64(method)<<32 | uint64(parent)
	if v, ok := l.cache[key]; ok {
		return v, nil
	}
	for i := len(l.runs) - 1; i >= 0; i-- {
		run := l.runs[i]
		from, to := uint64(0), run.Length
		for from < to {
			mid := from + (to-from)/2
			k, v, e := l.record(run, mid)
			if e != nil {
				return 0, e
			}
			if k == key {
				return v, nil
			}
			if k < key {
				from = mid + 1
			} else {
				to = mid
			}
		}
	}
	if l.next == ^uint32(0) {
		return 0, fmt.Errorf("heatmap LZ exceeds 32 bits")
	}
	l.cache[key] = l.next
	l.next++
	if len(l.cache) >= l.entryLimit {
		if e := l.flush(); e != nil {
			return 0, e
		}
	}
	return 0, nil
}
func (l *heatLZ) flush() error {
	if len(l.cache) == 0 {
		return nil
	}
	records := make([]heatRecord, 0, len(l.cache))
	for key, id := range l.cache {
		records = append(records, heatRecord{key, uint64(id)})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	f, e := heatTemp(l.dir)
	if e != nil {
		return e
	}
	w := bufio.NewWriterSize(f, 64*1024)
	for _, v := range records {
		if e = heatPut(w, v); e != nil {
			f.Close()
			return e
		}
	}
	if e = w.Flush(); e != nil {
		f.Close()
		return e
	}
	clear(l.cache)
	l.runs = append(l.runs, heatLZRun{f, l.runID, uint64(len(records))})
	l.runID++
	if len(l.runs) > l.runLimit {
		return l.merge()
	}
	return nil
}
func (l *heatLZ) merge() error {
	// Values are immutable node IDs, not multiplicities: merge without summing.
	readers := make([]*bufio.Reader, len(l.runs))
	var heap heatHeap
	for i, r := range l.runs {
		if _, e := r.File.Seek(0, io.SeekStart); e != nil {
			return e
		}
		readers[i] = bufio.NewReaderSize(r.File, 16*1024)
		v, ok, e := heatRawRead(readers[i])
		if e != nil {
			return e
		}
		if ok {
			heap.push(heatHeapItem{v, i})
		}
	}
	f, e := heatTemp(l.dir)
	if e != nil {
		return e
	}
	w := bufio.NewWriterSize(f, 64*1024)
	var previous heatRecord
	have := false
	count := uint64(0)
	for len(heap) > 0 {
		item := heap.pop()
		if have && previous.Key == item.Key {
			if previous.Count != item.Count {
				f.Close()
				return fmt.Errorf("inconsistent LZ edge")
			}
		} else {
			if e = heatPut(w, item.heatRecord); e != nil {
				f.Close()
				return e
			}
			previous = item.heatRecord
			have = true
			count++
		}
		v, ok, e := heatRawRead(readers[item.Input])
		if e != nil {
			f.Close()
			return e
		}
		if ok {
			heap.push(heatHeapItem{v, item.Input})
		}
	}
	if e = w.Flush(); e != nil {
		f.Close()
		return e
	}
	for _, r := range l.runs {
		r.File.Close()
	}
	l.runs = []heatLZRun{{f, l.runID, count}}
	l.runID++
	clear(l.pageSlots)
	l.pages = nil
	l.pageNext = 0
	return nil
}
