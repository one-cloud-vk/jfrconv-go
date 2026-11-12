// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"errors"
	"io"
	"regexp"
)

const Version = "0.2.0"

type Mode uint8

const (
	ModeExecution Mode = iota
	ModeAllocation
	ModeLive
	ModeLock
	ModeNative
	ModeTrace
	ModeNativeLock
)

// Options controls a conversion. Runtime memory limits are soft;
// the CLI supervisor enforces MaxRSS.
type Options struct {
	Mode                                         Mode
	CPU, CPUTime, Wall, Threads, Classify, Total bool
	Lines, BCI, Simple, Norm, Dot                bool
	State                                        []string
	From, To                                     int64
	Include, Exclude                             *regexp.Regexp
	Tag                                          string
	HasTag, Leak                                 bool
	LatencyNS                                    int64
	Grain, Tail                                  float64
	Skip                                         int
	Title, Highlight                             string
	HasTitle, HasHighlight                       bool
	MinWidth                                     float64
	Reverse, Inverted                            bool
	MemoryLimit, MaxRSS                          uint64
	MemoryStats                                  bool
	TempDir                                      string
}

func DefaultOptions() Options      { return Options{Title: "Flame Graph", Tail: .1, LatencyNS: -1} }
func (o Options) SpanFilter() bool { return o.LatencyNS >= 0 || o.HasTag }
func (o Options) ValueType() string {
	switch o.Mode {
	case ModeNative:
		return "malloc"
	case ModeAllocation, ModeLive:
		return "allocations"
	case ModeLock, ModeNativeLock:
		return "locks"
	default:
		return "cpu"
	}
}
func (o Options) TotalUnit() string {
	switch o.Mode {
	case ModeNative, ModeAllocation, ModeLive:
		return "bytes"
	default:
		return "nanoseconds"
	}
}

type Stats struct{ Samples, Units uint64 }

var ErrMemoryBudget = errors.New("conversion memory budget exceeded")

// Runner converts one input to the requested output format.
type Runner func(input string, out io.Writer, format string, options Options) (Stats, error)
