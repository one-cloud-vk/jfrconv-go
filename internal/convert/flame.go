// Portions derived from async-profiler 4.1 and jfrconv-rs.
// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"fmt"
	"io"
	"jfrconv-go/assets"
	"math/bits"
	"sort"
	"strconv"
	"strings"
)

type children struct{ starts, ids []uint32 }

func makeChildren(p *profile) children {
	c := children{make([]uint32, int(p.nodes.n)+1), make([]uint32, int(p.nodes.n)-1)}
	for i := uint32(1); i < p.nodes.n; i++ {
		c.starts[p.nodes.at(i).Parent+1]++
		c.ids[i-1] = i
	}
	for i := 1; i < len(c.starts); i++ {
		c.starts[i] += c.starts[i-1]
	}
	titles := p.sourceTitles
	if titles == nil {
		titles = make([]uint32, p.strings.refs.n)
		next := uint32(1)
		for id := uint32(1); id < p.nodes.n; id++ {
			name := p.frames.at(p.nodes.at(id).Frame).Name
			if name != 0 && titles[name] == 0 {
				titles[name] = next
				next++
			}
		}
	}
	bucket := func(n *node) uint32 {
		count := c.starts[n.Parent+1] - c.starts[n.Parent]
		if !p.htmlEncounter && count > 0 {
			count--
		}
		capacity := (uint64(count)*4 + 2) / 3
		if capacity < 16 {
			capacity = 16
		}
		capacity = uint64(1) << bits.Len64(capacity-1)
		if capacity > 1<<30 {
			capacity = 1 << 30
		}
		f := p.frames.at(n.Frame)
		key := titles[f.Name] | uint32(f.Kind)<<28
		return (key ^ (key >> 16)) & uint32(capacity-1)
	}
	sort.Slice(c.ids, func(i, j int) bool {
		a, b := c.ids[i], c.ids[j]
		x, y := p.nodes.at(a), p.nodes.at(b)
		if x.Parent != y.Parent {
			return x.Parent < y.Parent
		}
		cmp := p.strings.compare(p.frames.at(x.Frame).Name, p.frames.at(y.Frame).Name)
		if cmp != 0 {
			return cmp < 0
		}
		bx, by := bucket(x), bucket(y)
		if bx != by {
			return bx < by
		}
		if p.htmlEncounter {
			return a < b
		}
		return a > b
	})
	return c
}
func (c children) of(id uint32) []uint32 { return c.ids[c.starts[id]:c.starts[id+1]] }

type visit struct {
	id   uint32
	next int
	x    uint64
}

func walk(p *profile, c children, cutoff uint64, fn func(id uint32, level int, x uint64) error) error {
	if err := fn(0, 0, 0); err != nil {
		return err
	}
	stack := []visit{{x: p.nodes.at(0).Self}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		siblings := c.of(top.id)
		if top.next == len(siblings) {
			stack = stack[:len(stack)-1]
			continue
		}
		id := siblings[top.next]
		top.next++
		x := top.x
		n := p.nodes.at(id)
		top.x = satAdd(top.x, n.Total)
		if n.Total < cutoff {
			continue
		}
		if err := fn(id, len(stack), x); err != nil {
			return err
		}
		stack = append(stack, visit{id: id, x: satAdd(x, n.Self)})
	}
	return nil
}
func unsigned(w *bufio.Writer, n uint64) error {
	if w.Available() < 32 {
		if err := w.Flush(); err != nil {
			return err
		}
	}
	b := strconv.AppendUint(w.AvailableBuffer(), n, 10)
	_, err := w.Write(b)
	return err
}
func signed(w *bufio.Writer, n int64) error {
	if w.Available() < 32 {
		if err := w.Flush(); err != nil {
			return err
		}
	}
	b := strconv.AppendInt(w.AvailableBuffer(), n, 10)
	_, err := w.Write(b)
	return err
}
func suffix(kind uint8) string {
	switch kind {
	case 0:
		return "_[0]"
	case 1:
		return "_[j]"
	case 2:
		return "_[i]"
	case 5:
		return "_[k]"
	case 6:
		return "_[1]"
	}
	return ""
}
func writeCollapsed(p *profile, w *bufio.Writer, o Options) error {
	c := makeChildren(p)
	cutoff := uint64(float64(p.nodes.at(0).Total) * o.MinWidth / 100)
	var path []uint32
	return walk(p, c, cutoff, func(id uint32, level int, x uint64) error {
		if level == 0 {
			return nil
		}
		path = append(path[:level-1], id)
		n := p.nodes.at(id)
		if n.Self == 0 {
			return nil
		}
		for i, part := range path {
			if i > 0 {
				w.WriteByte(';')
			}
			w.WriteString(p.strings.text(p.frames.at(p.nodes.at(part).Frame).Name))
			w.WriteString(suffix(p.kind(part)))
		}
		w.WriteByte(' ')
		if p.isDiff {
			delta, defined := p.diffs[id]
			base := int64(0)
			if defined {
				base = int64(n.Self) - delta
			}
			if err := signed(w, base); err != nil {
				return err
			}
			w.WriteByte(' ')
		}
		if err := unsigned(w, n.Self); err != nil {
			return err
		}
		return w.WriteByte('\n')
	})
}
func jsString(w *bufio.Writer, s string) error {
	start := 0
	for i, r := range s {
		var esc string
		switch r {
		case '\\':
			esc = "\\\\"
		case '\'':
			esc = "\\'"
		case '\n':
			esc = "\\n"
		case '\r':
			esc = "\\r"
		case '\t':
			esc = "\\t"
		case '<':
			if i+1 < len(s) && s[i+1] == '/' {
				esc = "\\x3c"
			}
		case '\u2028':
			esc = "\\u2028"
		case '\u2029':
			esc = "\\u2029"
		default:
			if r < 32 || r == 127 {
				esc = fmt.Sprintf("\\u%04x", r)
			}
		}
		if esc != "" {
			if _, err := w.WriteString(s[start:i]); err != nil {
				return err
			}
			if _, err := w.WriteString(esc); err != nil {
				return err
			}
			start = i + len(string(r))
		}
	}
	_, err := w.WriteString(s[start:])
	return err
}

type template struct {
	tail string
	out  *bufio.Writer
}

func (t *template) until(marker string) error {
	at := strings.Index(t.tail, marker)
	if at < 0 {
		return fmt.Errorf("invalid upstream template: %s", marker)
	}
	if _, err := t.out.WriteString(t.tail[:at]); err != nil {
		return err
	}
	t.tail = t.tail[at+len(marker):]
	return nil
}
func writeHTML(p *profile, w *bufio.Writer, o Options) error {
	c := makeChildren(p)
	cutoff := uint64(float64(p.nodes.at(0).Total) * o.MinWidth / 100)
	depth := 1
	if err := walk(p, c, cutoff, func(_ uint32, level int, _ uint64) error {
		if level+1 > depth {
			depth = level + 1
		}
		return nil
	}); err != nil {
		return err
	}
	if cutoff <= 1 {
		depth = p.originalDepth + 1
	}
	t := template{assets.Flame, w}
	if err := t.until("/*height:*/300"); err != nil {
		return err
	}
	height := depth * 16
	if height > 32767 {
		height = 32767
	}
	unsigned(w, uint64(height))
	if err := t.until("/*title:*/"); err != nil {
		return err
	}
	title := o.Title
	if !o.HasTitle && o.Title == "Flame Graph" && p.hasSourceTitle {
		title = p.sourceTitle
	}
	w.WriteString(title)
	if err := t.until("/*inverted:*/false"); err != nil {
		return err
	}
	w.WriteString(strconv.FormatBool(o.Reverse != o.Inverted))
	if err := t.until("/*maxdiff:*/-1"); err != nil {
		return err
	}
	if p.isDiff {
		var max uint64
		for _, d := range p.diffs {
			v := uint64(d)
			if d < 0 {
				v = uint64(-(d + 1)) + 1
			}
			if v > max {
				max = v
			}
		}
		unsigned(w, max)
	} else {
		w.WriteString("-1")
	}
	if err := t.until("/*depth:*/0"); err != nil {
		return err
	}
	unsigned(w, uint64(depth))
	if err := t.until("/*cpool:*/"); err != nil {
		return err
	}
	ids := make([]uint32, int(p.strings.refs.n)-1)
	for i := range ids {
		ids[i] = uint32(i + 1)
	}
	sort.Slice(ids, func(i, j int) bool { return p.strings.compare(ids[i], ids[j]) < 0 })
	order := make([]uint32, p.strings.refs.n)
	w.WriteString("'all'")
	prev := ""
	for i, id := range ids {
		text := p.strings.text(id)
		common := 0
		for common < 95 && common < len(prev) && common < len(text) && prev[common] == text[common] && prev[common] < 128 {
			common++
		}
		w.WriteString(",\n'")
		if common+32 == 60 && strings.HasPrefix(text[common:], "/") {
			w.WriteString("\\x3c")
		} else {
			jsString(w, string(rune(common+32)))
		}
		jsString(w, text[common:])
		w.WriteByte('\'')
		order[id] = uint32(i + 1)
		prev = text
	}
	if err := t.until("/*frames:*/"); err != nil {
		return err
	}
	var lastX uint64
	if err := walk(p, c, cutoff, func(id uint32, level int, x uint64) error {
		n := p.nodes.at(id)
		f := p.frames.at(n.Frame)
		if p.isDiff {
			if d, ok := p.diffs[id]; ok {
				w.WriteString("d=")
				signed(w, d)
				w.WriteByte('\n')
			} else {
				w.WriteString("d=U\n")
			}
		}
		w.WriteString("f(")
		unsigned(w, uint64(order[f.Name])<<3|uint64(p.kind(id)))
		w.WriteByte(',')
		unsigned(w, uint64(level))
		w.WriteByte(',')
		signed(w, int64(x)-int64(lastX))
		w.WriteByte(',')
		unsigned(w, n.Total)
		ct := p.tiers[id]
		if (ct.Inlined|ct.C1|ct.Interpreted) != 0 && ct.Inlined < n.Total && ct.Interpreted < n.Total {
			for _, v := range [3]uint64{ct.Inlined, ct.C1, ct.Interpreted} {
				w.WriteByte(',')
				unsigned(w, v)
			}
		}
		w.WriteString(")\n")
		lastX = x
		return nil
	}); err != nil {
		return err
	}
	if err := t.until("/*highlight:*/"); err != nil {
		return err
	}
	if o.HasHighlight {
		w.WriteByte('\'')
		jsString(w, o.Highlight)
		w.WriteByte('\'')
	}
	_, err := io.WriteString(w, t.tail)
	return err
}
