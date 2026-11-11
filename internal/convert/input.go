// Copyright The async-profiler authors.
// Portions derived from jfrconv-rs. SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const textLineLimit = 16 << 20
const textDepthLimit = 65536

func readText(path string, o Options, p *profile) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 128*1024)
	peek, err := reader.Peek(512)
	if err != nil && err != io.EOF {
		return err
	}
	prefix := strings.TrimSpace(strings.TrimPrefix(string(peek), "\uFEFF"))
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".html" || extension == ".htm" || strings.HasPrefix(prefix, "<!DOCTYPE") || strings.HasPrefix(prefix, "<!doctype") || strings.HasPrefix(prefix, "<html") {
		return readHTML(reader, o, p)
	}
	return readCollapsed(reader, o, p)
}

// ReadSlice bounds each incremental allocation before accepting another piece.
// The buffer is reused for unusually long rows; a whole HTML file is never read.
type profileLines struct {
	reader *bufio.Reader
	buffer []byte
}

func (r *profileLines) next() (string, bool, error) {
	r.buffer = r.buffer[:0]
	for {
		part, err := r.reader.ReadSlice('\n')
		if len(r.buffer)+len(part) > textLineLimit {
			return "", false, fmt.Errorf("profile input line exceeds 16 MiB limit")
		}
		if len(r.buffer) == 0 && err != bufio.ErrBufferFull {
			if err != nil && err != io.EOF {
				return "", false, err
			}
			if len(part) == 0 {
				return "", false, nil
			}
			if !utf8.Valid(part) {
				return "", false, fmt.Errorf("profile input is not valid UTF-8")
			}
			return strings.TrimRight(string(part), "\r\n"), true, nil
		}
		r.buffer = append(r.buffer, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil && err != io.EOF {
			return "", false, err
		}
		if len(r.buffer) == 0 {
			return "", false, nil
		}
		if !utf8.Valid(r.buffer) {
			return "", false, fmt.Errorf("profile input is not valid UTF-8")
		}
		return strings.TrimRight(string(r.buffer), "\r\n"), true, nil
	}
}

func threadRole(name string, kind uint8) uint8 {
	if kind != 3 || !strings.HasPrefix(name, "[") || !strings.HasSuffix(name, "]") {
		return 0
	}
	inner := name[1 : len(name)-1]
	tid := ""
	if strings.HasPrefix(inner, "tid=") {
		tid = inner[4:]
	} else if at := strings.LastIndex(inner, " tid="); at >= 0 {
		tid = inner[at+5:]
	}
	if tid == "" {
		return 0
	}
	for _, c := range tid {
		if c < '0' || c > '9' {
			return 0
		}
	}
	return 1
}

func inputTypeName(name string) (string, uint8) {
	for _, suffix := range []struct {
		text string
		kind uint8
	}{{"_[j]", 1}, {"_[i]", 2}, {"_[k]", 5}, {"_[0]", 0}, {"_[1]", 6}} {
		if strings.HasSuffix(name, suffix.text) {
			return strings.TrimSuffix(name, suffix.text), suffix.kind
		}
	}
	if strings.Contains(name, "::") || strings.HasPrefix(name, "-[") || strings.HasPrefix(name, "+[") {
		return name, 4
	}
	first, _ := utf8.DecodeRuneInString(name)
	if strings.IndexByte(name, '/') > 0 && !strings.HasPrefix(name, "[") || strings.IndexByte(name, '.') > 0 && first <= 0xffff && unicode.IsUpper(first) {
		return name, 1
	}
	return name, 3
}

func readCollapsed(reader *bufio.Reader, o Options, p *profile) error {
	lines := profileLines{reader: reader}
	number := 0
	var frames []uint32
	for {
		text, ok, err := lines.next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		number++
		if strings.TrimSpace(text) == "" {
			continue
		}
		at := strings.LastIndexByte(text, ' ')
		if at < 0 {
			return fmt.Errorf("collapsed line %d lacks a sample count", number)
		}
		amount, err := strconv.ParseUint(text[at+1:], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid collapsed sample count on line %d", number)
		}
		frames = frames[:0]
		stack := text[:at]
		for {
			if len(frames) >= textDepthLimit {
				return fmt.Errorf("collapsed stack exceeds 65536 frames")
			}
			raw, tail, more := strings.Cut(stack, ";")
			name, kind := inputTypeName(raw)
			frames = append(frames, p.frameName(name, kind, 0, 0, threadRole(name, kind)))
			if !more {
				break
			}
			stack = tail
		}
		ids, keep := transformPath(frames, o, p)
		if keep {
			path := p.path(ids, true)
			p.credit(path, amount, amount, amount, 0)
		}
	}
}

type htmlLevel struct {
	frame     uint32
	hasFrame  bool
	remaining uint64
}

func flushHTML(levels *[]htmlLevel, o Options, p *profile) error {
	if len(*levels) == 0 {
		return fmt.Errorf("invalid HTML frame nesting")
	}
	top := (*levels)[len(*levels)-1]
	if top.remaining != 0 {
		frames := make([]uint32, 0, len(*levels)-1)
		for _, level := range *levels {
			if level.hasFrame {
				frames = append(frames, level.frame)
			}
		}
		keep := true
		if !p.htmlEncounter {
			frames, keep = transformPath(frames, o, p)
		}
		if keep {
			path := p.path(frames, true)
			p.credit(path, top.remaining, top.remaining, top.remaining, 0)
		}
	}
	*levels = (*levels)[:len(*levels)-1]
	return nil
}

func readHTML(reader *bufio.Reader, o Options, p *profile) error {
	lines := profileLines{reader: reader}
	foundPool := false
	for {
		text, ok, err := lines.next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if strings.HasPrefix(strings.TrimLeftFunc(text, unicode.IsSpace), "const cpool") {
			foundPool = true
			break
		}
	}
	if !foundPool {
		return fmt.Errorf("HTML has no async-profiler constant pool")
	}
	var pool []string
	previous := ""
	poolEnd := false
	for {
		text, ok, err := lines.next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		row := strings.TrimSpace(text)
		if row == "];" || row == "]" {
			poolEnd = true
			break
		}
		if row == "" {
			continue
		}
		packed, err := quotedHTML(row)
		if err != nil {
			return err
		}
		if len(pool) == 0 {
			pool = append(pool, packed)
			continue
		}
		prefix, n := utf8.DecodeRuneInString(packed)
		if prefix < 32 || prefix > 127 {
			return fmt.Errorf("invalid HTML constant-pool prefix")
		}
		count := int(prefix) - 32
		if count > len(previous) {
			return fmt.Errorf("HTML constant-pool prefix exceeds prior ASCII name")
		}
		for _, c := range []byte(previous[:count]) {
			if c >= 128 {
				return fmt.Errorf("HTML constant-pool prefix exceeds prior ASCII name")
			}
		}
		value := previous[:count] + packed[n:]
		previous = value
		pool = append(pool, value)
	}
	if !poolEnd || len(pool) == 0 {
		return fmt.Errorf("truncated HTML constant pool")
	}
	preserve := !o.Reverse && o.Include == nil && o.Exclude == nil
	p.htmlEncounter = preserve
	var levels []htmlLevel
	var total uint64
	currentLevel := 0
	sawFrames, sawRoot, framesEnd := false, false, false
	for {
		text, ok, err := lines.next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		row := strings.TrimSpace(text)
		if strings.HasPrefix(row, "d=") {
			continue
		}
		if len(row) < 2 || (row[0] != 'f' && row[0] != 'u' && row[0] != 'n') || row[1] != '(' {
			if sawFrames {
				framesEnd = true
				break
			}
			continue
		}
		sawFrames = true
		if !strings.HasSuffix(row, ")") {
			return fmt.Errorf("invalid HTML frame row")
		}
		numbers := strings.Split(row[2:len(row)-1], ",")
		key, err := strconv.ParseUint(numbers[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid HTML frame key")
		}
		rest := numbers[1:]
		start := 0
		if row[0] == 'f' {
			if len(rest) < 2 {
				return fmt.Errorf("HTML f row lacks level/offset")
			}
			level, err := strconv.ParseUint(rest[0], 10, 32)
			if err != nil || level >= textDepthLimit {
				return fmt.Errorf("invalid HTML frame depth")
			}
			currentLevel = int(level)
			if !signed128Decimal(rest[1]) {
				return fmt.Errorf("invalid HTML frame offset")
			}
			start = 2
		} else if row[0] == 'u' {
			currentLevel++
		}
		if currentLevel >= textDepthLimit || currentLevel > len(levels) {
			return fmt.Errorf("invalid HTML frame depth")
		}
		if currentLevel > p.originalDepth {
			p.originalDepth = currentLevel
		}
		if len(rest) > start+4 {
			return fmt.Errorf("HTML frame has too many counters")
		}
		if len(rest) > start {
			total, err = strconv.ParseUint(rest[start], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid HTML frame width")
			}
		}
		kind := uint8(key & 7)
		if kind > 6 {
			return fmt.Errorf("invalid HTML frame type")
		}
		extraStart := start + 1
		if extraStart > len(rest) {
			extraStart = len(rest)
		}
		extra := rest[extraStart:]
		if len(extra) > 0 && (kind <= 2 || kind >= 6) {
			kind = 1
		}
		var counters [3]uint64
		var sum uint64
		for i, value := range extra {
			counters[i], err = strconv.ParseUint(value, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid HTML subtype counter")
			}
			if math.MaxUint64-sum < counters[i] {
				return fmt.Errorf("HTML Java subtype counters exceed frame total")
			}
			sum += counters[i]
		}
		if sum > total {
			return fmt.Errorf("HTML Java subtype counters exceed frame total")
		}
		if !preserve {
			if counters[0] >= ceilDiv(total, 3) {
				kind = 2
			} else if counters[1] >= ceilDiv(total, 2) {
				kind = 6
			} else if counters[2] >= ceilDiv(total, 2) {
				kind = 0
			}
		}
		index := key >> 3
		if index >= uint64(len(pool)) {
			return fmt.Errorf("HTML frame constant index out of bounds")
		}
		name := pool[index]
		for len(levels) > currentLevel {
			if err := flushHTML(&levels, o, p); err != nil {
				return err
			}
		}
		if currentLevel == 0 {
			if sawRoot {
				return fmt.Errorf("HTML contains multiple root frames")
			}
			sawRoot = true
		} else {
			parent := &levels[len(levels)-1]
			if total > parent.remaining {
				return fmt.Errorf("HTML child totals exceed parent total")
			}
			parent.remaining -= total
		}
		level := htmlLevel{remaining: total}
		if currentLevel != 0 {
			level.hasFrame = true
			level.frame = p.frameName(name, kind, 0, 0, threadRole(name, kind))
		}
		levels = append(levels, level)
		if preserve && currentLevel != 0 && len(extra) > 0 {
			frames := make([]uint32, 0, len(levels)-1)
			for _, level := range levels {
				if level.hasFrame {
					frames = append(frames, level.frame)
				}
			}
			target := p.path(frames, true).Leaf
			if p.tiers == nil {
				p.tiers = make(map[uint32]tierCounts)
			}
			counts, exists := p.tiers[target]
			if !exists {
				p.budget.reserve(64)
			}
			counts.Inlined = satAdd(counts.Inlined, counters[0])
			counts.C1 = satAdd(counts.C1, counters[1])
			counts.Interpreted = satAdd(counts.Interpreted, counters[2])
			p.tiers[target] = counts
		}
	}
	if !sawFrames || !framesEnd {
		return fmt.Errorf("truncated HTML frame data")
	}
	for len(levels) > 0 {
		if err := flushHTML(&levels, o, p); err != nil {
			return err
		}
	}
	p.budget.reserve(uint64(p.strings.refs.n) * 4)
	p.sourceTitles = make([]uint32, p.strings.refs.n)
	for i := range p.sourceTitles {
		p.sourceTitles[i] = math.MaxUint32
	}
	p.sourceTitles[0] = 0
	for id, name := range pool {
		if id == 0 {
			continue
		}
		if nameID, ok := p.strings.lookup(name); ok {
			p.sourceTitles[nameID] = uint32(id)
		}
	}
	return nil
}

func signed128Decimal(text string) bool {
	negative := strings.HasPrefix(text, "-")
	if negative || strings.HasPrefix(text, "+") {
		text = text[1:]
	}
	if text == "" {
		return false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return false
		}
	}
	text = strings.TrimLeft(text, "0")
	if len(text) < 39 {
		return true
	}
	if len(text) > 39 {
		return false
	}
	max := "170141183460469231731687303715884105727"
	if negative {
		max = "170141183460469231731687303715884105728"
	}
	return text <= max
}

func quotedHTML(row string) (string, error) {
	row = strings.TrimSuffix(row, ",")
	if len(row) < 2 || row[0] != '\'' || row[len(row)-1] != '\'' {
		return "", fmt.Errorf("invalid quoted HTML constant")
	}
	raw := row[1 : len(row)-1]
	var result strings.Builder
	result.Grow(len(raw))
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			r, n := utf8.DecodeRuneInString(raw[i:])
			if r == utf8.RuneError && n == 1 {
				return "", fmt.Errorf("invalid UTF-8 in JavaScript constant")
			}
			result.WriteRune(r)
			i += n
			continue
		}
		i++
		if i >= len(raw) {
			return "", fmt.Errorf("truncated JavaScript string escape")
		}
		esc := raw[i]
		i++
		switch esc {
		case '\\', '\'', '"':
			result.WriteByte(esc)
		case 'n':
			result.WriteByte('\n')
		case 'r':
			result.WriteByte('\r')
		case 't':
			result.WriteByte('\t')
		case 'b':
			result.WriteByte('\b')
		case 'f':
			result.WriteByte('\f')
		case 'v':
			result.WriteByte('\v')
		case '0':
			result.WriteByte(0)
		case 'x', 'u':
			count := 2
			if esc == 'u' {
				count = 4
			}
			if len(raw)-i < count {
				return "", fmt.Errorf("truncated JavaScript hexadecimal escape")
			}
			value, err := strconv.ParseUint(raw[i:i+count], 16, 32)
			if err != nil {
				return "", fmt.Errorf("invalid JavaScript hexadecimal escape")
			}
			i += count
			if value >= 0xd800 && value <= 0xdbff {
				if len(raw)-i < 6 || raw[i:i+2] != "\\u" {
					return "", fmt.Errorf("unpaired JavaScript high surrogate")
				}
				low, err := strconv.ParseUint(raw[i+2:i+6], 16, 32)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return "", fmt.Errorf("invalid JavaScript low surrogate")
				}
				i += 6
				value = 0x10000 + ((value - 0xd800) << 10) + low - 0xdc00
			} else if value >= 0xdc00 && value <= 0xdfff {
				return "", fmt.Errorf("unpaired JavaScript low surrogate")
			}
			result.WriteRune(rune(value))
		default:
			return "", fmt.Errorf("unsupported JavaScript string escape")
		}
	}
	return result.String(), nil
}
