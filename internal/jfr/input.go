// Copyright The async-profiler authors
// Copyright 2026 jfrconv-go contributors
// SPDX-License-Identifier: Apache-2.0
package jfr

import (
	"fmt"
	"io"
	"os"
	"unicode/utf16"
	"unicode/utf8"
)

const headerSize uint64 = 68
const bufferSize = 128 * 1024
const maxString = 16 * 1024 * 1024
const maxItems = 16 * 1024 * 1024

func invalid(s string) error { return fmt.Errorf("invalid JFR: %s", s) }

// Sticky errors remove repeated error plumbing from the hot primitive decoder.
// Every selected event is checked before its callback can observe any values.
type input struct {
	file           *os.File
	buffer         []byte
	base           uint64
	cursor, filled int
	limit          uint64
	err            error
}

func newInput(file *os.File, limit uint64) input {
	return input{file: file, buffer: make([]byte, bufferSize), limit: limit}
}
func (in *input) position() uint64 { return in.base + uint64(in.cursor) }
func (in *input) fail(err error) {
	if in.err == nil {
		in.err = err
	}
}
func (in *input) seek(pos uint64) {
	if in.err != nil {
		return
	}
	if pos > in.limit {
		in.fail(invalid("seek exceeds record boundary"))
		return
	}
	if pos >= in.base && pos <= in.base+uint64(in.filled) {
		in.cursor = int(pos - in.base)
		return
	}
	if _, err := in.file.Seek(int64(pos), io.SeekStart); err != nil {
		in.fail(err)
		return
	}
	in.base = pos
	in.cursor = 0
	in.filled = 0
}
func (in *input) refill() bool {
	remaining := in.filled - in.cursor
	copy(in.buffer, in.buffer[in.cursor:in.filled])
	in.base += uint64(in.cursor)
	in.cursor = 0
	in.filled = remaining
	n, err := in.file.Read(in.buffer[remaining:])
	in.filled += n
	if n > 0 {
		return true
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	in.fail(err)
	return false
}
func (in *input) take(dst []byte) {
	if in.err != nil {
		return
	}
	if uint64(len(dst)) > in.limit-in.position() {
		in.fail(invalid("truncated record"))
		return
	}
	for len(dst) > 0 {
		if in.cursor == in.filled && !in.refill() {
			return
		}
		n := copy(dst, in.buffer[in.cursor:in.filled])
		in.cursor += n
		dst = dst[n:]
	}
}
func (in *input) b() byte {
	if in.err != nil {
		return 0
	}
	if in.position() >= in.limit {
		in.fail(invalid("truncated record"))
		return 0
	}
	if in.cursor == in.filled && !in.refill() {
		return 0
	}
	v := in.buffer[in.cursor]
	in.cursor++
	return v
}

func var9(b []byte, at int) (uint64, int) {
	_ = b[at+8]
	n := uint64(0)
	for shift := uint(0); shift < 56; shift += 7 {
		v := b[at]
		at++
		n |= uint64(v&127) << shift
		if v < 128 {
			return n, at
		}
	}
	return n | uint64(b[at])<<56, at + 1
}
func bufferedVar(b []byte, at int) (uint64, int) {
	if b[at] < 128 {
		return uint64(b[at]), at + 1
	}
	return var9(b, at)
}
func (in *input) u() uint64 {
	if in.err != nil {
		return 0
	}
	if in.cursor < in.filled && in.position() < in.limit {
		v := in.buffer[in.cursor]
		if v < 128 {
			in.cursor++
			return uint64(v)
		}
		if in.filled-in.cursor >= 9 {
			n, end := var9(in.buffer, in.cursor)
			if uint64(end-in.cursor) > in.limit-in.position() {
				in.fail(invalid("truncated varint"))
				return 0
			}
			in.cursor = end
			return n
		}
	}
	n := uint64(0)
	for shift := uint(0); shift < 56; shift += 7 {
		b := in.b()
		n |= uint64(b&127) << shift
		if b < 128 {
			return n
		}
	}
	return n | uint64(in.b())<<56
}
func (in *input) count() int {
	n := in.u()
	if n > maxItems || n > in.limit-in.position() {
		in.fail(invalid("invalid or excessive element count"))
		return 0
	}
	return int(n)
}
func (in *input) record(offset, end uint64) (uint64, uint64) {
	in.limit = end
	if in.position() != offset {
		in.seek(offset)
	}
	size := in.u()
	if in.err != nil {
		return 0, offset
	}
	if size < 2 || size > end-offset || offset+size <= in.position() {
		in.fail(invalid("invalid record size"))
		return 0, offset
	}
	in.limit = offset + size
	id := in.u()
	return id, in.limit
}

// refString appends constant-pool bytes directly to the chunk arena.
func (in *input) refString(cp map[uint64]strRef, a *byteArena) (strRef, bool) {
	switch tag := in.b(); tag {
	case 0:
		return strRef{}, false
	case 1:
		return strRef{}, true
	case 2:
		id := in.u()
		r, ok := cp[id]
		if !ok && id != 0 {
			in.fail(invalid("missing string constant"))
		}
		return r, ok
	case 3, 5:
		n := in.count()
		if in.err != nil {
			return strRef{}, false
		}
		if n > maxString {
			in.fail(invalid("string exceeds 16 MiB"))
			return strRef{}, false
		}
		r, b, err := a.alloc(n)
		if err != nil {
			in.fail(err)
			return strRef{}, false
		}
		in.take(b)
		if tag == 3 {
			if utf8.Valid(b) {
				return r, true
			}
			size := javaUTF8Size(b)
			out := b
			if size > len(b) {
				r, out, err = a.alloc(size)
				if err != nil {
					in.fail(err)
					return strRef{}, false
				}
			}
			javaUTF8Into(out, b)
			return r, true
		}
		size := len(b)
		for _, v := range b {
			if v >= 128 {
				size++
			}
		}
		if size == len(b) {
			return r, true
		}
		r, out, err := a.alloc(size)
		if err != nil {
			in.fail(err)
			return strRef{}, false
		}
		at := 0
		for _, v := range b {
			at += utf8.EncodeRune(out[at:], rune(v))
		}
		return r, true
	case 4:
		n := in.count()
		if in.err != nil {
			return strRef{}, false
		}
		if n > maxString/2 {
			in.fail(invalid("UTF-16 string too long"))
			return strRef{}, false
		}
		r, b, err := a.alloc(n * 3)
		if err != nil {
			in.fail(err)
			return strRef{}, false
		}
		used := 0
		var pending uint16
		write := func(v rune) { used += utf8.EncodeRune(b[used:], v) }
		for i := 0; i < n && in.err == nil; i++ {
			v := in.u()
			if v > 65535 {
				in.fail(invalid("invalid UTF-16 code unit"))
				break
			}
			c := uint16(v)
			if pending != 0 {
				if c >= 0xdc00 && c <= 0xdfff {
					write(utf16.DecodeRune(rune(pending), rune(c)))
					pending = 0
					continue
				}
				write(utf8.RuneError)
				pending = 0
			}
			if c >= 0xd800 && c <= 0xdbff {
				pending = c
			} else if c >= 0xdc00 && c <= 0xdfff {
				write(utf8.RuneError)
			} else {
				write(rune(c))
			}
		}
		if pending != 0 {
			write(utf8.RuneError)
		}
		r.length = uint32(used)
		return r, true
	default:
		in.fail(invalid(fmt.Sprintf("invalid string encoding %d", tag)))
		return strRef{}, false
	}
}

// Java's UTF-8 decoder replaces a malformed sequence's valid prefix, rather
// than merging adjacent errors or replacing every byte. JNI Span tags can
// contain modified UTF-8 surrogate triples and overlong NUL bytes.
func javaUTF8Step(b []byte) (n int, malformed bool) {
	continuation := func(v byte) bool { return v&0xc0 == 0x80 }
	_, n = utf8.DecodeRune(b)
	if n != 1 || b[0] < utf8.RuneSelf {
		return n, false
	}
	lead := b[0]
	if lead >= 0xe0 && lead <= 0xef && len(b) >= 2 && continuation(b[1]) && (lead != 0xe0 || b[1] >= 0xa0) {
		n = 2
		if len(b) >= 3 && continuation(b[2]) {
			n = 3
		}
	} else if lead >= 0xf0 && lead <= 0xf4 && len(b) >= 2 && continuation(b[1]) && (lead != 0xf0 || b[1] >= 0x90) && (lead != 0xf4 || b[1] < 0x90) {
		n = 2
		if len(b) >= 3 && continuation(b[2]) {
			n = 3
		}
	}
	return n, true
}

func javaUTF8Size(b []byte) int {
	size := 0
	for len(b) != 0 {
		n, malformed := javaUTF8Step(b)
		if malformed {
			size += 3
		} else {
			size += n
		}
		b = b[n:]
	}
	return size
}

func javaUTF8Into(out, b []byte) {
	at := 0
	for len(b) != 0 {
		n, malformed := javaUTF8Step(b)
		if malformed {
			out[at], out[at+1], out[at+2] = 0xef, 0xbf, 0xbd
			at += 3
		} else {
			at += copy(out[at:], b[:n])
		}
		b = b[n:]
	}
}

// valueString is transient: inline event tags never enter the chunk arena.
func (in *input) valueString(c *Chunk) string {
	v, _ := in.valueStringPresent(c)
	return v
}

func (in *input) valueStringPresent(c *Chunk) (string, bool) {
	var arena = byteArena{exact: true}
	// A CP reference belongs to the original arena, while inline text is local.
	if in.err != nil {
		return "", false
	}
	if in.cursor == in.filled && !in.refill() {
		return "", false
	}
	if in.position() >= in.limit {
		in.fail(invalid("truncated string"))
		return "", false
	}
	if in.buffer[in.cursor] == 2 {
		in.cursor++
		id := in.u()
		r, ok := c.strings[id]
		if !ok {
			if id != 0 {
				in.fail(invalid("missing event string constant"))
			}
			return "", false
		}
		return c.text.string(r), true
	}
	r, present := in.refString(nil, &arena)
	return arena.string(r), present
}

func (in *input) discardString(c *Chunk) {
	switch tag := in.b(); tag {
	case 0, 1:
	case 2:
		id := in.u()
		if id != 0 {
			if _, ok := c.strings[id]; !ok {
				in.fail(invalid("missing string constant"))
			}
		}
	case 3, 5:
		n := in.count()
		if n > maxString {
			in.fail(invalid("string exceeds 16 MiB"))
			return
		}
		in.seek(in.position() + uint64(n))
	case 4:
		n := in.count()
		if n > maxString/2 {
			in.fail(invalid("UTF-16 string too long"))
			return
		}
		for i := 0; i < n && in.err == nil; i++ {
			if in.u() > 65535 {
				in.fail(invalid("invalid UTF-16 code unit"))
			}
		}
	default:
		in.fail(invalid(fmt.Sprintf("invalid string encoding %d", tag)))
	}
}
