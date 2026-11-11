// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bufio"
	"bytes"
	"io"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func textProfileMap(t *testing.T, p *profile) map[string]uint64 {
	t.Helper()
	var bytes bytes.Buffer
	writer := bufio.NewWriter(&bytes)
	if err := writeCollapsed(p, writer, DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]uint64)
	for _, row := range strings.Split(strings.TrimSpace(bytes.String()), "\n") {
		if row == "" {
			continue
		}
		at := strings.LastIndexByte(row, ' ')
		if at < 0 {
			t.Fatalf("invalid collapsed export %q", row)
		}
		value, err := strconv.ParseUint(row[at+1:], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		result[row[:at]] += value
	}
	return result
}

func textProfileHTML(t *testing.T, p *profile, o Options) string {
	t.Helper()
	var bytes bytes.Buffer
	writer := bufio.NewWriter(&bytes)
	if err := writeHTML(p, writer, o); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	return bytes.String()
}

func TestTextImportsCapturedJava41HTMLWithUnicodeAndReusedWidths(t *testing.T) {
	o := DefaultOptions()
	p := newProfile(o)
	if err := readText(filepath.Join("..", "..", "tests", "fixtures", "wall-v41.html"), o, p); err != nil {
		t.Fatal(err)
	}
	if p.nodes.at(0).Total != 20 {
		t.Fatalf("total%d", p.nodes.at(0).Total)
	}
	want := map[string]uint64{
		"[worker-α tid=1];demo/Main.run_[j];demo/Worker.work_[j]":      7,
		"[worker-α tid=1];second/Main.run_[j];second/Worker.work_[j]":  7,
		"[io-worker tid=2];demo/Main.run_[0];demo/Worker.work_[i]":     3,
		"[io-worker tid=2];second/Main.run_[0];second/Worker.work_[i]": 3,
	}
	if got := textProfileMap(t, p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got%v want%v", got, want)
	}
	if !p.htmlEncounter || len(p.sourceTitles) != int(p.strings.refs.n) {
		t.Fatal("lost original HTML dictionary ordering")
	}
}

func TestTextHTMLRoundTripPreservesEscapesAndMixedCompilationCounters(t *testing.T) {
	o := DefaultOptions()
	p := newProfile(o)
	name := "pkg/Frame.α'\\<\n"
	java := p.frameName(name, 1, 0, 0, 0)
	inline := p.frameName(name, 2, 0, 0, 0)
	p.credit(p.path([]uint32{java}, true), 5, 5, 5, 0)
	p.credit(p.path([]uint32{inline}, true), 2, 2, 2, 0)
	source := textProfileHTML(t, p, o)
	imported := newProfile(o)
	if err := readHTML(bufio.NewReader(strings.NewReader(source)), o, imported); err != nil {
		t.Fatal(err)
	}
	if imported.nodes.at(0).Total != 7 || imported.nodes.n != 2 {
		t.Fatalf("bad roundtrip total/nodes: %d/%d", imported.nodes.at(0).Total, imported.nodes.n)
	}
	node := imported.nodes.at(1)
	if node.Total != 7 || imported.tiers[1].Inlined != 2 || imported.strings.text(imported.frames.at(node.Frame).Name) != name {
		t.Fatal("lost subtype counts or escaped name")
	}
}

func TestTextHTMLRebuildKeepsEffectiveC1AndInterpretedColors(t *testing.T) {
	o := DefaultOptions()
	p := newProfile(o)
	root := p.frameName("Root.call", 1, 0, 0, 0)
	for _, sample := range []struct {
		name   string
		kind   uint8
		weight uint64
	}{{"C1.method", 6, 1}, {"C1.method", 1, 1}, {"Interp.method", 0, 3}, {"Interp.method", 1, 1}} {
		frame := p.frameName(sample.name, sample.kind, 0, 0, 0)
		p.credit(p.path([]uint32{root, frame}, true), sample.weight, sample.weight, sample.weight, 0)
	}
	source := textProfileHTML(t, p, o)
	o.Reverse = true
	o.Skip = 1
	imported := newProfile(o)
	if err := readHTML(bufio.NewReader(strings.NewReader(source)), o, imported); err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{"C1.method_[1]": 2, "Interp.method_[0]": 4}
	if got := textProfileMap(t, imported); !reflect.DeepEqual(got, want) {
		t.Fatalf("got%v want%v", got, want)
	}
}

func TestTextDirectHTMLIgnoresSkipAndRetainsOriginalTitleIDs(t *testing.T) {
	source := "const cpool = [\n'all',\n' root',\n' unused',\n' worker'\n];\nf(3,0,0,5)\nu(11,5)\nu(27,5)\n\n"
	o := DefaultOptions()
	o.Skip = 99
	p := newProfile(o)
	if err := readHTML(bufio.NewReader(strings.NewReader(source)), o, p); err != nil {
		t.Fatal(err)
	}
	if got := textProfileMap(t, p); !reflect.DeepEqual(got, map[string]uint64{"root;worker": 5}) {
		t.Fatal(got)
	}
	if _, exists := p.strings.lookup("unused"); exists {
		t.Fatal("unused CP name was retained globally")
	}
	id, exists := p.strings.lookup("worker")
	if !exists || p.sourceTitles[id] != 3 {
		t.Fatalf("lost source CP ID: %v", p.sourceTitles)
	}
}

func TestTextCollapsedReverseRetainsThreadGroupingAndTypes(t *testing.T) {
	o := DefaultOptions()
	o.Reverse = true
	p := newProfile(o)
	if err := readCollapsed(bufio.NewReader(strings.NewReader("[α tid=12];pkg/Root.run_[0];C::native 4\n")), o, p); err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{"[α tid=12];C::native;pkg/Root.run_[0]": 4}
	if got := textProfileMap(t, p); !reflect.DeepEqual(got, want) {
		t.Fatalf("got%v want%v", got, want)
	}
	if _, kind := inputTypeName("java/lang/Foo.run"); kind != 1 {
		t.Fatal(kind)
	}
	if _, kind := inputTypeName("malloc"); kind != 3 {
		t.Fatal(kind)
	}
}

func TestTextMalformedInputsFailWithoutPanic(t *testing.T) {
	for _, source := range []string{"", "const cpool = [\n'all'\n", "const cpool = [\n'all',\n'!x'\n];\n", "const cpool = [\n'all'\n];\nf(3,2,0,1)\n\n", "const cpool = [\n'all'\n];\nf(3,0,0,1,2)\n\n"} {
		o := DefaultOptions()
		if err := readHTML(bufio.NewReader(strings.NewReader(source)), o, newProfile(o)); err == nil {
			t.Fatalf("accepted malformed HTML %q", source)
		}
	}
	for _, source := range []string{"root nope\n", "root -1\n", "root\n", strings.Repeat("a;", textDepthLimit) + "z 1\n"} {
		o := DefaultOptions()
		if err := readCollapsed(bufio.NewReaderSize(strings.NewReader(source), 128*1024), o, newProfile(o)); err == nil {
			t.Fatal("accepted malformed collapsed input")
		}
	}
	for _, source := range []string{"'bad\\xXX'", "'\\ud800'", "'\\udc00'", "'\\ud800\\u0041'"} {
		if _, err := quotedHTML(source); err == nil {
			t.Fatalf("accepted %q", source)
		}
	}
	if got, err := quotedHTML("'\\ud83d\\ude00'"); err != nil || got != "😀" {
		t.Fatalf("got%q %v", got, err)
	}
	if !signed128Decimal("-170141183460469231731687303715884105728") || signed128Decimal("170141183460469231731687303715884105728") {
		t.Fatal("incorrect signed offset bounds")
	}
}

type repeatingTextByte struct{}

func (repeatingTextByte) Read(buffer []byte) (int, error) {
	for i := range buffer {
		buffer[i] = 'x'
	}
	return len(buffer), nil
}
func TestTextLineLimitRejectsBeforeReadingWholeOversizedLine(t *testing.T) {
	lines := profileLines{reader: bufio.NewReaderSize(io.LimitReader(repeatingTextByte{}, textLineLimit+1000000), 128*1024)}
	if _, _, err := lines.next(); err == nil {
		t.Fatal("accepted an oversized profile row")
	}
}
