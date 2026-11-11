// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"jfrconv-go/internal/jfr"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unicode/utf16"
)

func TestStringArenaOwnershipAndJavaUTF16Order(t *testing.T) {
	s := newStringPool(nil)
	ids := make([]uint32, 12000)
	names := make([]string, len(ids))
	for i := range ids {
		names[i] = fmt.Sprintf("pkg/Example%05d.method/long_symbol_immutable", i)
		ids[i] = s.intern(names[i])
	}
	first := s.text(ids[0])
	for i, id := range ids {
		if got := s.text(id); got != names[i] {
			t.Fatalf("arena changed %d: %q", i, got)
		}
		if again := s.intern(names[i]); again != id {
			t.Fatalf("duplicate ID for %d", i)
		}
	}
	if first != names[0] {
		t.Fatal("old arena alias changed")
	}
	values := []string{"z", "a", "\ue000", "𐀀", "😀", "\uffff", "é", "e\u0301"}
	var order []uint32
	for _, v := range values {
		order = append(order, s.intern(v))
	}
	sort.Slice(order, func(i, j int) bool { return s.compare(order[i], order[j]) < 0 })
	sort.Slice(values, func(i, j int) bool {
		a, b := utf16.Encode([]rune(values[i])), utf16.Encode([]rune(values[j]))
		for n := 0; n < len(a) && n < len(b); n++ {
			if a[n] != b[n] {
				return a[n] < b[n]
			}
		}
		return len(a) < len(b)
	})
	for i, id := range order {
		if s.text(id) != values[i] {
			t.Fatalf("UTF16 sort %d: %q != %q", i, s.text(id), values[i])
		}
	}
}

type budgetHeatSession struct {
	file   *os.File
	parent *profile
}

func (h *budgetHeatSession) BeforeChunk(*jfr.Chunk) error {
	h.parent.budget.reserve(h.parent.budget.limit + 1)
	return nil
}
func (h *budgetHeatSession) Add(*jfr.Chunk, *jfr.Event) error { return nil }
func (h *budgetHeatSession) AfterChunk(*jfr.Chunk) error      { return nil }
func (h *budgetHeatSession) Finish() error                    { return nil }
func (h *budgetHeatSession) Write(*bufio.Writer) error        { return nil }
func (h *budgetHeatSession) Close()                           { h.file.Close() }
func (h *budgetHeatSession) ResolveRetained(*jfr.Chunk, *jfr.Event) (cachedPath, bool, error) {
	return cachedPath{}, false, nil
}
func (h *budgetHeatSession) Retained(uint32, uint64, uint64, uint64) error { return nil }
func TestMajorBudgetPanicPreservesCleanupAndDiffError(t *testing.T) {
	dir := t.TempDir()
	o := DefaultOptions()
	o.MemoryLimit = 16 << 20
	o.TempDir = dir
	saved := makeHeatmap
	defer func() { makeHeatmap = saved }()
	makeHeatmap = func(_ string, p *profile, _ Options) (heatmapSession, error) {
		f, err := p.temp()
		if err != nil {
			return nil, err
		}
		return &budgetHeatSession{f, p}, nil
	}
	_, err := Run(filepath.Join("..", "..", "tests", "fixtures", "mixed.jfr"), io.Discard, "heatmap", o)
	if !errors.Is(err, ErrMemoryBudget) {
		t.Fatalf("budget error: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("scratch retained after allocation failure: %v %v", files, err)
	}
	input := filepath.Join(dir, "profile.collapsed")
	if err = os.WriteFile(input, []byte("root;leaf 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	o.MemoryLimit = 1
	var output bytes.Buffer
	_, err = RunDiff(input, input, &output, "html", o)
	if !errors.Is(err, ErrMemoryBudget) || output.Len() != 0 {
		t.Fatalf("diff budget must be a clean error: %v output=%d", err, output.Len())
	}
}
