// Portions derived from async-profiler 4.1 and jfrconv-rs.
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"jfrconv-go/internal/jfr"
	"math"
	"math/bits"
	"sort"
	"strings"
)

type interval struct{ Start, End uint64 }
type spanFilter map[uint64][]interval

func mulDiv(v, m, d uint64) uint64 {
	if d == 0 {
		return 0
	}
	hi, lo := bits.Mul64(v, m)
	if hi >= d {
		return math.MaxUint64
	}
	q, _ := bits.Div64(hi, lo, d)
	return q
}
func eventNS(c *jfr.Chunk, ticks uint64) uint64 {
	if ticks >= c.StartTicks {
		return satAdd(c.StartNS, mulDiv(ticks-c.StartTicks, 1_000_000_000, c.TicksPerSec))
	}
	delta := mulDiv(c.StartTicks-ticks, 1_000_000_000, c.TicksPerSec)
	if delta > c.StartNS {
		return 0
	}
	return c.StartNS - delta
}
func readSpans(path string, o Options) (spanFilter, error) {
	r, err := jfr.Open(path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	r.Budget = o.MemoryLimit
	f := make(spanFilter)
	found := false
	for {
		c, err := r.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, parserError(err)
		}
		err = r.Events(c, jfr.MaskSpan, func(e *jfr.Event) error {
			if e.Kind != jfr.Span {
				return nil
			}
			found = true
			duration := mulDiv(e.Duration, 1_000_000_000, c.TicksPerSec)
			if o.HasLatency && float64(duration) < o.Latency*1_000_000 {
				return nil
			}
			if o.Tag != nil && (!e.TagPresent || !o.Tag.MatchString(e.Tag)) {
				return nil
			}
			t := eventNS(c, e.Time)
			f[e.TID] = append(f[e.TID], interval{t, satAdd(t, duration)})
			return nil
		})
		if err != nil {
			return nil, parserError(err)
		}
	}
	if !found {
		return nil, fmt.Errorf("no profiler.Span / profiler.MethodTrace events found for span filtering")
	}
	for tid, pairs := range f {
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].Start == pairs[j].Start {
				return pairs[i].End < pairs[j].End
			}
			return pairs[i].Start < pairs[j].Start
		})
		n := 0
		for _, pair := range pairs {
			if n > 0 && pair.Start <= pairs[n-1].End {
				if pair.End > pairs[n-1].End {
					pairs[n-1].End = pair.End
				}
			} else {
				pairs[n] = pair
				n++
			}
		}
		if n == 0 {
			f[tid] = nil
		} else if cap(pairs) > 2*n {
			// Sorting/merging can turn thousands of nested or repeated spans
			// into one interval. Do not retain the entire input-sized backing
			// array for the subsequent sample-filtering pass.
			compacted := make([]interval, n)
			copy(compacted, pairs[:n])
			f[tid] = compacted
		} else {
			f[tid] = pairs[:n]
		}
	}
	return f, nil
}
func (f spanFilter) contains(tid, t uint64) bool {
	pairs := f[tid]
	at := sort.Search(len(pairs), func(i int) bool { return pairs[i].Start > t })
	return at > 0 && t <= pairs[at-1].End
}
func stateIDs(c *jfr.Chunk, o Options) (ids []uint64, restricted bool, err error) {
	if o.Mode != ModeExecution {
		return nil, false, nil
	}
	states := c.Enums["jdk.types.ThreadState"]
	if o.State != nil {
		for _, prefix := range o.State {
			found := false
			var best uint64
			for id, name := range states {
				if strings.HasPrefix(name, "STATE_") && strings.HasPrefix(name[6:], prefix) {
					if !found || id < best {
						best = id
					}
					found = true
				}
			}
			if !found {
				return nil, true, fmt.Errorf("unknown thread state %s", prefix)
			}
			ids = append(ids, best)
		}
		return ids, true, nil
	}
	if o.CPU || o.Wall {
		for id, name := range states {
			if (name == "STATE_DEFAULT") == o.CPU {
				ids = append(ids, id)
			}
		}
		return ids, true, nil
	}
	return nil, false, nil
}
func selected(e *jfr.Event, o Options) bool {
	switch o.Mode {
	case ModeExecution:
		return e.Kind == jfr.Execution
	case ModeAllocation:
		return e.Kind == jfr.Allocation
	case ModeLive:
		return e.Kind == jfr.Live
	case ModeLock:
		return e.Kind == jfr.Lock
	case ModeNative:
		return e.Kind == jfr.Malloc || e.Kind == jfr.Free
	case ModeTrace:
		return e.Kind == jfr.Span
	}
	return false
}
func eventMask(o Options) jfr.Mask {
	switch o.Mode {
	case ModeAllocation:
		return jfr.MaskAllocation
	case ModeLive:
		return jfr.MaskLive
	case ModeLock:
		return jfr.MaskLock
	case ModeNative:
		return jfr.MaskNative
	case ModeTrace:
		return jfr.MaskSpan
	default:
		return jfr.MaskExecution
	}
}
func timeBound(ms int64, start, end uint64) int64 {
	base := uint64(0)
	if ms < 0 {
		base = end
	} else if ms < 1_500_000_000_000 {
		base = start
	}
	if ms > math.MaxInt64/1_000_000 {
		return math.MaxInt64
	}
	if ms < math.MinInt64/1_000_000 {
		return math.MinInt64
	}
	delta := ms * 1_000_000
	if delta >= 0 {
		if base > uint64(math.MaxInt64-delta) {
			return math.MaxInt64
		}
		return int64(base) + delta
	}
	if base > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(base) + delta
}
func normalizeValue(value uint64, c *jfr.Chunk, o Options) uint64 {
	if o.Mode == ModeLock || o.Mode == ModeTrace {
		return uint64(float64(value) * (1e9 / float64(c.TicksPerSec)))
	}
	return value
}
func writeRecord(w *bufio.Writer, values [5]uint64) error {
	if w.Available() < 40 {
		if err := w.Flush(); err != nil {
			return err
		}
	}
	b := w.AvailableBuffer()
	for _, n := range values {
		b = binary.LittleEndian.AppendUint64(b, n)
	}
	_, err := w.Write(b)
	return err
}

type outstanding struct {
	Path        *cachedPath
	Time, Value uint64
	Thread      uint32
}
type pathKey struct{ Stack, TID uint64 }

func readJFR(path string, o Options, p *profile, format string, heat heatmapSession) error {
	var filter spanFilter
	var err error
	if o.SpanFilter() {
		filter, err = readSpans(path, o)
		if err != nil {
			return err
		}
	}
	r, err := jfr.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()
	start, end := r.StartNS, r.EndNS
	p.StartNS = start
	p.EndNS = end
	var spool *bufio.Writer
	if p.spool != nil && format == "otlp" {
		spool = bufio.NewWriterSize(p.spool, 128*1024)
	}
	otlp := format == "otlp"
	needsTime := o.From != 0 || o.To != 0 || filter != nil || o.Leak || heat != nil
	var live map[uint64]outstanding
	if o.Leak {
		live = make(map[uint64]outstanding)
	}
	minTime, maxTime := uint64(math.MaxUint64), uint64(0)
	var observedEnd uint64
	for {
		available, budgetErr := p.readerBudget()
		if budgetErr != nil {
			return budgetErr
		}
		r.Budget = available
		c, err := r.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			return parserError(err)
		}
		err = func() error {
			if c.EndNS > observedEnd {
				observedEnd = c.EndNS
			}
			if otlp && !o.Leak {
				if err := writeRecord(spool, [5]uint64{c.StartNS, math.MaxUint64, c.EndNS, 0, 0}); err != nil {
					return err
				}
			}
			from, to := int64(math.MinInt64), int64(math.MaxInt64)
			if o.From != 0 {
				from = timeBound(o.From, start, observedEnd)
			}
			if o.To != 0 {
				to = timeBound(o.To, start, observedEnd)
			}
			states, restricted, err := stateIDs(c, o)
			if err != nil {
				return err
			}
			var agg *aggregator
			if !o.Leak && heat == nil {
				agg = newAggregator(o.Threads)
			}
			var resolver pathResolver
			if heat == nil {
				resolver.names = make(nameCache)
			}
			var native *nativeSorter
			if o.Leak {
				native = &nativeSorter{dir: o.TempDir}
				defer native.close()
			}
			if heat != nil {
				if err := heat.BeforeChunk(c); err != nil {
					return err
				}
			}
			err = r.Events(c, eventMask(o), func(e *jfr.Event) error {
				if !selected(e, o) {
					return nil
				}
				var t uint64
				if needsTime {
					t = eventNS(c, e.Time)
				}
				if int64(t) < from || int64(t) > to {
					return nil
				}
				if restricted {
					found := false
					for _, id := range states {
						if e.State == id {
							found = true
							break
						}
					}
					if !found {
						return nil
					}
				}
				if filter != nil && !filter.contains(e.TID, t) {
					return nil
				}
				if o.Leak {
					if t < minTime {
						minTime = t
					}
					if t > maxTime {
						maxTime = t
					}
					size := e.Value
					if e.Kind == jfr.Free {
						size = 0
					}
					return native.push(nativeRecord{Time: e.Time, Address: e.Address, Size: size, TID: e.TID, StackID: e.StackID})
				}
				if heat != nil {
					return heat.Add(c, e)
				}
				agg.push(e)
				return nil
			})
			if err != nil {
				return parserError(err)
			}
			if o.Leak {
				paths := make(map[pathKey]*cachedPath)
				err = native.each(func(record nativeRecord) error {
					if record.Size == 0 {
						delete(live, record.Address)
						return nil
					}
					key := pathKey{Stack: record.StackID}
					if o.Threads {
						key.TID = record.TID
					}
					cached, ok := paths[key]
					if !ok {
						e := jfr.Event{Kind: jfr.Malloc, Time: record.Time, TID: record.TID, StackID: record.StackID, Samples: 1, Value: record.Size, Address: record.Address}
						var resolved cachedPath
						var exists bool
						if heat != nil {
							var resolveErr error
							resolved, exists, resolveErr = heat.ResolveRetained(c, &e)
							if resolveErr != nil {
								return resolveErr
							}
						} else {
							resolved, exists = resolver.resolve(c, &e, o, p, format)
						}
						if exists {
							cached = &resolved
						}
						paths[key] = cached
					}
					if cached == nil {
						delete(live, record.Address)
						return nil
					}
					thread := uint32(0)
					if otlp {
						thread = p.strings.intern(threadName(c, record.TID))
					}
					live[record.Address] = outstanding{cached, eventNS(c, record.Time), record.Size, thread}
					return nil
				})
				native.close()
				if err != nil {
					return err
				}
			} else if heat == nil {
				err = agg.each(o.Grain, func(a aggregate) error {
					e := a.Event.expand()
					resolved, ok := resolver.resolve(c, &e, o, p, format)
					if !ok {
						return nil
					}
					value := normalizeValue(a.Value, c, o)
					amount := a.Samples
					if o.Total {
						amount = value
					}
					t := eventNS(c, e.Time)
					p.credit(resolved, amount, a.Samples, value, t)
					if otlp {
						thread := p.strings.intern(threadName(c, e.TID))
						return writeRecord(spool, [5]uint64{t, uint64(resolved.Leaf), a.Samples, value, uint64(thread)})
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
			if heat != nil {
				if err := heat.AfterChunk(c); err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	if o.Leak && minTime != math.MaxUint64 {
		if otlp {
			if err := writeRecord(spool, [5]uint64{start, math.MaxUint64, end, 0, 0}); err != nil {
				return err
			}
		}
		cutoff := satAdd(minTime, uint64(float64(maxTime-minTime)*(1-o.Tail)))
		type group struct {
			Path               *cachedPath
			Count, Value, Time uint64
			Thread             uint32
		}
		groups := make(map[uint32]group)
		for _, v := range live {
			if v.Time > cutoff {
				continue
			}
			g, ok := groups[v.Path.Leaf]
			if !ok {
				g.Path = v.Path
				g.Time = v.Time
				g.Thread = v.Thread
			}
			g.Count++
			g.Value = satAdd(g.Value, v.Value)
			if v.Time < g.Time {
				g.Time = v.Time
			}
			groups[v.Path.Leaf] = g
		}
		ids := make([]uint32, 0, len(groups))
		for id := range groups {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		fraction := 0.
		for _, id := range ids {
			g := groups[id]
			count, value := g.Count, g.Value
			if o.Grain > 0 {
				scaled := float64(count) / o.Grain
				rounded := uint64(scaled)
				fraction += scaled - float64(rounded)
				if fraction >= 1 {
					fraction--
					rounded++
				}
				value = uint64(float64(value) * float64(rounded) / float64(count))
				count = rounded
			}
			if count == 0 {
				continue
			}
			amount := count
			if o.Total {
				amount = value
			}
			p.credit(*g.Path, amount, count, value, g.Time)
			if heat != nil {
				if err := heat.Retained(id, count, value, g.Time); err != nil {
					return err
				}
			}
			if otlp {
				if err := writeRecord(spool, [5]uint64{g.Time, uint64(id), count, value, uint64(g.Thread)}); err != nil {
					return err
				}
			}
		}
	}
	if spool != nil {
		return spool.Flush()
	}
	return nil
}
