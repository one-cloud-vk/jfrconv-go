// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"errors"
	"io"
	"regexp"
)

const Version = "0.1.1"

type Mode uint8

const (
	ModeExecution Mode = iota
	ModeAllocation
	ModeLive
	ModeLock
	ModeNative
	ModeTrace
)

// Options is the shared, immutable per-invocation contract. Runtime memory
// limits are soft; MaxRSS is enforced by the CLI's separate supervisor.
type Options struct {
	Mode                                Mode
	CPU, Wall, Threads, Classify, Total bool
	Lines, BCI, Simple, Norm, Dot       bool
	State                               []string
	From, To                            int64
	Include, Exclude, Tag               *regexp.Regexp
	Grain, Tail, Latency                float64
	HasLatency, Leak                    bool
	Skip                                int
	Title, Highlight                    string
	HasHighlight                        bool
	MinWidth                            float64
	Reverse, Inverted                   bool
	MemoryLimit, MaxRSS                 uint64
	MemoryStats                         bool
	TempDir                             string
}

func DefaultOptions() Options      { return Options{Title: "Flame Graph", Tail: .1} }
func (o Options) SpanFilter() bool { return o.HasLatency || o.Tag != nil }
func (o Options) ValueType() string {
	switch o.Mode {
	case ModeNative:
		return "malloc"
	case ModeAllocation, ModeLive:
		return "allocations"
	case ModeLock:
		return "locks"
	case ModeTrace:
		return "wall"
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

// The CLI calls Run/RunDiff with a staged output and publishes only after
// success. Their implementations are supplied by the conversion engine.
type Runner func(input string, out io.Writer, format string, options Options) (Stats, error)
