// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeatmapNativeLeakWritesOnlyRetainedPhysicalRecords(t *testing.T) {
	o := DefaultOptions()
	o.Mode, o.Leak, o.Tail, o.Total, o.TempDir = ModeNative, true, 0, true, t.TempDir()
	var output bytes.Buffer
	stats, err := Run("../../tests/fixtures/leak-order.jfr", &output, "heatmap", o)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Units != 4096 || stats.Samples != 1 {
		t.Fatalf("retained counters credited twice or lost: %+v", stats)
	}
	text := output.String()
	marker := `<pre id="executionsHeatmap">`
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatal("retained heatmap was empty")
	}
	start += len(marker)
	end := strings.Index(text[start:], "</pre>")
	if end < 0 {
		t.Fatal("missing native heatmap payload")
	}
	payload := []byte(text[start : start+end])
	if len(payload) < 6 || payload[len(payload)-1] != 'E' {
		t.Fatal("truncated native heatmap payload")
	}
	var count uint64
	for i, value := range payload[len(payload)-6 : len(payload)-1] {
		switch value {
		case 127:
			value = 0
		case 126:
			value = 13
		case 125:
			value = 38
		case 124:
			value = 60
		case 123:
			value = 62
		}
		count |= uint64(value) << (i * 6)
	}
	if count != 1 {
		t.Fatalf("native payload has %d samples, want one retained address", count)
	}
}

func TestHeatmapIdentityKeepsEqualVisibleNativeNamesDistinct(t *testing.T) {
	p := newProfile(DefaultOptions())
	p.heat = newHeatReference()
	name := p.strings.intern("native")
	a := p.heatFrame(name, p.strings.intern("ClassA"), name, 3, 0, 0)
	b := p.heatFrame(name, p.strings.intern("ClassB"), name, 3, 0, 0)
	if a == b {
		t.Fatal("semantic classes collapsed into visible label")
	}
	pa, pb := p.path([]uint32{a}, false), p.path([]uint32{b}, false)
	if pa.Leaf == pb.Leaf {
		t.Fatal("exact paths merged semantic method identities")
	}
	if leaf, ok := p.lookupExactPath([]uint32{a}); !ok || leaf != pa.Leaf {
		t.Fatal("missing exact path")
	}
}

func TestHeatmapReferenceMatchesRecordedJavaIDsAndRGB(t *testing.T) {
	for _, fixture := range []struct {
		Name    string
		Samples uint64
		Methods int
	}{{"heatmap-order", 10, 11}, {"heatmap-start", 5, 4}} {
		t.Run(fixture.Name, func(t *testing.T) {
			o := DefaultOptions()
			o.CPU = true
			o.TempDir = t.TempDir()
			var output bytes.Buffer
			stats, e := Run(filepath.Join("../../tests/fixtures", fixture.Name+".jfr"), &output, "heatmap", o)
			if e != nil {
				t.Fatal(e)
			}
			if stats.Samples != fixture.Samples {
				t.Fatalf("samples %d", stats.Samples)
			}
			file := filepath.Join(t.TempDir(), "native.html")
			if e = os.WriteFile(file, output.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
			node, e := exec.LookPath("node")
			if e != nil {
				t.Skip("Node unavailable: native conversion passed; original JS color oracle needs Node")
			}
			script := "const fs=require('fs'),assert=require('assert/strict'),colors=require(process.argv[1]);assert.deepEqual(colors(process.argv[3]),JSON.parse(fs.readFileSync(process.argv[2],'utf8')));"
			helper, _ := filepath.Abs("../../tools/heatmap_colors.js")
			golden, _ := filepath.Abs(filepath.Join("../../tests/fixtures", fixture.Name+".colors.json"))
			result, e := exec.Command(node, "-e", script, helper, golden, file).CombinedOutput()
			if e != nil {
				t.Fatalf("original color oracle: %v\n%s", e, result)
			}
			var expected []map[string]any
			b, e := os.ReadFile(golden)
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(b, &expected); e != nil || len(expected) != fixture.Methods {
				t.Fatalf("golden invalid: %v", e)
			}
		})
	}
}

func TestHeatmap45WeightedWallAndFirstOccupiedBlock(t *testing.T) {
	o := DefaultOptions()
	o.Wall = true
	o.TempDir = t.TempDir()
	var output bytes.Buffer
	stats, err := Run("../../tests/fixtures/heatmap-wall45.jfr", &output, "heatmap", o)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Samples != 6 || !strings.Contains(output.String(), "1699999999981") {
		t.Fatalf("lost wall multiplicity or shifted the first time block: %+v", stats)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("original JavaScript timeline decoder requires Node")
	}
	file := filepath.Join(t.TempDir(), "wall.html")
	if err = os.WriteFile(file, output.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	decoded, err := exec.Command(node, "../../tools/heatmap_decode.js", file).CombinedOutput()
	if err != nil {
		t.Fatalf("timeline decoder: %v\n%s", err, decoded)
	}
	golden, err := os.ReadFile("../../tests/fixtures/heatmap-wall45.timeline.json")
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected map[string]any
	if err = json.Unmarshal(decoded, &actual); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(golden, &expected); err != nil {
		t.Fatal(err)
	}
	actualJSON, _ := json.Marshal(actual)
	expectedJSON, _ := json.Marshal(expected)
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf("Java 4.5 weighted timeline differs:\n%s\n%s", actualJSON, expectedJSON)
	}
}

func TestHeatmapLZSpillPreservesEdgesAndCapsCaches(t *testing.T) {
	l := newHeatLZ(t.TempDir())
	defer l.close()
	l.entryLimit = 7
	l.runLimit = 2
	l.pageLimit = 2
	expected := map[uint64]uint32{}
	next := uint32(1)
	for round := 0; round < 3; round++ {
		for i := uint32(1); i < 1200; i++ {
			parent, method := i%53, (i*37)%997+1
			key := uint64(method)<<32 | uint64(parent)
			got, e := l.append(parent, method)
			if e != nil {
				t.Fatal(e)
			}
			if old, ok := expected[key]; ok {
				if got != old {
					t.Fatalf("edge %d got%d want%d", key, got, old)
				}
			} else {
				if got != 0 {
					t.Fatal("new edge returned existing ID")
				}
				expected[key] = next
				next++
			}
			if len(l.cache) > 7 || len(l.runs) > 2 || len(l.pages) > 2 {
				t.Fatal("cache limit exceeded")
			}
		}
	}
	if l.next != next {
		t.Fatal("node IDs changed during spill")
	}
}

func TestHeatmapWeightedSortCoalescesExternalRuns(t *testing.T) {
	s := heatSorter{dir: t.TempDir()}
	defer s.close()
	expected := map[uint64]uint64{}
	for i := heatRunEntries*2 + 137; i >= 0; i-- {
		key := uint64(i%79)<<32 | uint64(i%113)
		if e := s.push(key, 1); e != nil {
			t.Fatal(e)
		}
		expected[key]++
	}
	f, e := s.finish()
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var previous uint64
	first := true
	for {
		v, ok, e := heatRead(r)
		if e != nil {
			t.Fatal(e)
		}
		if !ok {
			break
		}
		if !first && v.Key <= previous {
			t.Fatal("unsorted or duplicate key")
		}
		if expected[v.Key] != v.Count {
			t.Fatalf("count%d expected%d", v.Count, expected[v.Key])
		}
		delete(expected, v.Key)
		previous = v.Key
		first = false
	}
	if len(expected) != 0 {
		t.Fatal("lost weighted records")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		t.Fatal(e)
	}
}

func TestHeatmapFrequencyUsesLZEdgesAndSkipsMillionRepeats(t *testing.T) {
	p := newProfile(DefaultOptions())
	p.heat = newHeatReference()
	root := p.heatFrame(p.strings.intern("Root.run"), p.strings.intern("Root"), p.strings.intern("run"), 1, 0, 0)
	child := p.heatFrame(p.strings.intern("Child.work"), p.strings.intern("Child"), p.strings.intern("work"), 1, 0, 0)
	leaf := p.path([]uint32{root, child}, false).Leaf
	p.heat.methodRanks = make([][2]uint32, p.frames.n)
	for i := range p.heat.methodRanks {
		p.heat.methodRanks[i] = [2]uint32{heatUnknown, heatUnknown}
	}
	p.heat.method(root, true)
	p.heat.method(child, false)
	p.heat.leaves = []uint32{leaf}
	p.heat.stackRanks = make([]uint32, p.nodes.n)
	keys, e := heatTemp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer keys.Close()
	w := bufio.NewWriter(keys)
	if e = heatPut(w, heatRecord{0, 20_000_000}); e != nil {
		t.Fatal(e)
	}
	if e = w.Flush(); e != nil {
		t.Fatal(e)
	}
	tree, e := newHeatTree(p)
	if e != nil {
		t.Fatal(e)
	}
	h := exactHeatmap{p: p, ref: p.heat, o: Options{TempDir: t.TempDir()}}
	d := heatPrepared{keys: keys}
	methods, e := h.ordered(&tree, &d)
	if e != nil {
		t.Fatal(e)
	}
	if methods.ids[child] != 1 || methods.ids[tree.starts[root]] != 2 {
		t.Fatalf("sample frequency used instead of LZ edges: %v", methods.ids)
	}
}
