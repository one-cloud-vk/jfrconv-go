// Portions derived from async-profiler 4.1 and jfrconv-rs.
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"jfrconv-go/internal/jfr"
	"os"
	"path/filepath"
	"strings"
)

// The exact heatmap backend owns reference-order metadata, weighted spools and
// its bounded LZ index. Event is borrowed only for the duration of Add.
type heatmapSession interface {
	BeforeChunk(*jfr.Chunk) error
	Add(*jfr.Chunk, *jfr.Event) error
	AfterChunk(*jfr.Chunk) error
	Finish() error
	Write(*bufio.Writer) error
	Close()
	ResolveRetained(*jfr.Chunk, *jfr.Event) (cachedPath, bool, error)
	Retained(leaf uint32, count, value, timeNS uint64) error
}

var makeHeatmap = newHeatmap

func readProfile(path string, o Options, format string) (p *profile, heat heatmapSession, err error) {
	defer func() {
		cause := recover()
		if err != nil || cause != nil {
			if heat != nil {
				heat.Close()
			}
			if p != nil {
				p.close()
			}
		}
		if cause != nil {
			panic(cause)
		}
	}()
	p = newProfile(o)
	f, e := os.Open(path)
	if e != nil {
		return p, nil, e
	}
	var magic [4]byte
	n, e := f.Read(magic[:])
	f.Close()
	if e != nil && e != io.EOF {
		return p, nil, e
	}
	if strings.EqualFold(filepath.Ext(path), ".jfr") || n == 4 && string(magic[:]) == "FLR\x00" {
		if format == "heatmap" {
			heat, err = makeHeatmap(path, p, o)
			if err != nil {
				return p, heat, err
			}
		}
		if format == "otlp" {
			p.spool, err = p.temp()
			if err != nil {
				return p, heat, err
			}
		}
		err = readJFR(path, o, p, format, heat)
		if err == nil && heat != nil {
			err = heat.Finish()
		}
	} else {
		if format != "html" && format != "collapsed" {
			return p, nil, fmt.Errorf("%s input requires JFR", format)
		}
		err = readText(path, o, p)
	}
	return p, heat, err
}

func writeProfile(p *profile, w *bufio.Writer, format string, o Options) error {
	switch format {
	case "html":
		return writeHTML(p, w, o)
	case "collapsed":
		return writeCollapsed(p, w, o)
	case "pprof":
		return writePprof(p, w, o)
	case "pb.gz":
		return writePprofGzip(p, w, o)
	case "otlp":
		return writeOTLP(p, w, o)
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
}

func Run(input string, out io.Writer, format string, o Options) (stats Stats, err error) {
	defer func() {
		if r := recover(); r != nil {
			if r == ErrMemoryBudget {
				err = ErrMemoryBudget
			} else {
				panic(r)
			}
		}
	}()
	p, heat, err := readProfile(input, o, format)
	if err != nil {
		return stats, err
	}
	defer p.close()
	if heat != nil {
		defer heat.Close()
	}
	stats.Units = p.nodes.at(0).Total
	for id := uint32(0); id < p.nodes.n; id++ {
		stats.Samples = satAdd(stats.Samples, p.nodes.at(id).Samples)
	}
	p.seal()
	w := bufio.NewWriterSize(out, 128*1024)
	if heat != nil {
		err = heat.Write(w)
	} else {
		err = writeProfile(p, w, format, o)
	}
	if err == nil {
		err = w.Flush()
	}
	return stats, err
}

func RunDiff(base, current string, out io.Writer, format string, o Options) (stats Stats, err error) {
	defer func() {
		if cause := recover(); cause != nil {
			if cause == ErrMemoryBudget {
				err = ErrMemoryBudget
			} else {
				panic(cause)
			}
		}
	}()
	if format != "html" && format != "collapsed" {
		return stats, fmt.Errorf("--diff supports HTML and collapsed output")
	}
	b, _, err := readProfile(base, o, "collapsed")
	if err != nil {
		return stats, err
	}
	defer b.close()
	p, _, err := readProfile(current, o, "collapsed")
	if err != nil {
		return stats, err
	}
	defer p.close()
	p.isDiff = true
	p.diffs = make(map[uint32]int64)
	// Look up each current path in the base trie using semantic frame names/kinds.
	matched := make([]uint32, p.nodes.n)
	defined := make([]bool, p.nodes.n)
	defined[0] = true
	for id := uint32(0); id < p.nodes.n; id++ {
		n := p.nodes.at(id)
		var found bool
		var bn uint32
		if id == 0 {
			found = true
		} else if defined[n.Parent] {
			f := p.frames.at(n.Frame)
			name, exists := b.strings.lookup(p.strings.text(f.Name))
			if exists {
				candidate := *f
				candidate.Name = name
				mask := uint64(len(b.frameSlots) - 1)
				at := frameHash(candidate) & mask
				for {
					slot := b.frameSlots[at]
					if slot == 0 {
						break
					}
					if *b.frames.at(slot - 1) == candidate {
						frameID := slot - 1
						edgeAt := mix64(uint64(matched[n.Parent])<<32|uint64(frameID)) & uint64(len(b.edgeSlots)-1)
						for {
							e := b.edgeSlots[edgeAt]
							if e == 0 {
								break
							}
							v := b.nodes.at(e)
							if v.Parent == matched[n.Parent] && v.Frame == frameID {
								bn = e
								found = true
								break
							}
							edgeAt = (edgeAt + 1) & uint64(len(b.edgeSlots)-1)
						}
						break
					}
					at = (at + 1) & mask
				}
			}
		}
		if found {
			matched[id] = bn
			defined[id] = true
			p.diffs[id] = int64(n.Self) - int64(b.nodes.at(bn).Self)
		}
	}
	stats.Units = p.nodes.at(0).Total
	p.seal()
	w := bufio.NewWriterSize(out, 128*1024)
	err = writeProfile(p, w, format, o)
	if err == nil {
		err = w.Flush()
	}
	return stats, err
}

func parserError(err error) error {
	if errors.Is(err, jfr.ErrBudget) {
		return ErrMemoryBudget
	}
	return err
}
