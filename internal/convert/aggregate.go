// SPDX-License-Identifier: Apache-2.0
package convert

import "jfrconv-go/internal/jfr"

type representative struct {
	Time, TID, StackID, ClassID, MethodID uint64
	Kind                                  jfr.EventKind
	TLAB                                  bool
}
type aggregate struct {
	Event          representative
	Samples, Value uint64
}
type aggregator struct {
	slots   []uint32
	items   []aggregate
	threads bool
}

const noSlot = ^uint32(0)

func newAggregator(threads bool) *aggregator {
	a := &aggregator{slots: make([]uint32, 1024), threads: threads}
	for i := range a.slots {
		a.slots[i] = noSlot
	}
	return a
}
func (a *aggregator) hash(e representative) uint32 {
	h := uint32(e.StackID)
	if e.Kind == jfr.MethodTrace {
		h += uint32(e.MethodID) * 127
	}
	if e.Kind == jfr.Allocation || e.Kind == jfr.Live || e.Kind == jfr.Lock {
		h += uint32(e.ClassID) * 127
		if e.Kind == jfr.Allocation && !e.TLAB {
			h += 17
		}
	}
	if a.threads {
		h += uint32(e.TID) * 31
	}
	return h
}
func (a *aggregator) same(x, y representative) bool {
	native := (x.Kind == jfr.Malloc || x.Kind == jfr.Free) && (y.Kind == jfr.Malloc || y.Kind == jfr.Free)
	return x.StackID == y.StackID && (!a.threads || x.TID == y.TID) && x.MethodID == y.MethodID && (native || x.Kind == y.Kind && ((x.Kind != jfr.Allocation && x.Kind != jfr.Live && x.Kind != jfr.Lock) || x.ClassID == y.ClassID) && (x.Kind != jfr.Allocation || x.TLAB == y.TLAB))
}
func (a *aggregator) push(e *jfr.Event) {
	r := representative{e.Time, e.TID, e.StackID, e.ClassID, e.MethodID, e.Kind, e.TLAB}
	at := a.hash(r) & uint32(len(a.slots)-1)
	for {
		id := a.slots[at]
		if id == noSlot {
			a.slots[at] = uint32(len(a.items))
			a.items = append(a.items, aggregate{r, e.Samples, e.Value})
			break
		}
		if a.same(a.items[id].Event, r) {
			v := &a.items[id]
			v.Samples = satAdd(v.Samples, e.Samples)
			v.Value = satAdd(v.Value, e.Value)
			return
		}
		at = (at + 1) & uint32(len(a.slots)-1)
	}
	if len(a.items)*2 > len(a.slots) {
		old := a.slots
		a.slots = make([]uint32, len(old)*2)
		for i := range a.slots {
			a.slots[i] = noSlot
		}
		for _, id := range old {
			if id == noSlot {
				continue
			}
			at := a.hash(a.items[id].Event) & uint32(len(a.slots)-1)
			for a.slots[at] != noSlot {
				at = (at + 1) & uint32(len(a.slots)-1)
			}
			a.slots[at] = id
		}
	}
}
func (a *aggregator) each(grain float64, fn func(aggregate) error) error {
	fraction := 0.
	for _, id := range a.slots {
		if id == noSlot {
			continue
		}
		v := a.items[id]
		if grain > 0 {
			count := float64(v.Samples) / grain
			rounded := uint64(count)
			fraction += count - float64(rounded)
			if fraction >= 1 {
				fraction--
				rounded++
			}
			if v.Samples == 0 {
				v.Value = 0
			} else {
				v.Value = uint64(float64(v.Value) * float64(rounded) / float64(v.Samples))
			}
			v.Samples = rounded
			if rounded == 0 {
				continue
			}
		}
		if err := fn(v); err != nil {
			return err
		}
	}
	return nil
}
func (r representative) expand() jfr.Event {
	return jfr.Event{Kind: r.Kind, Time: r.Time, TID: r.TID, StackID: r.StackID, ClassID: r.ClassID, MethodID: r.MethodID, TLAB: r.TLAB, Samples: 1}
}
