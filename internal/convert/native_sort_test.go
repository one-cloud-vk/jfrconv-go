// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"errors"
	"os"
	"testing"
)

func TestNativeSortMultipleMergePassesAndCleanup(t *testing.T) {
	dir := t.TempDir()
	s := nativeSorter{dir: dir, runItems: 8}
	defer s.close()
	const count = 8*(mergeFanIn+3) + 5
	for i := 0; i < count; i++ {
		if err := s.push(nativeRecord{Time: uint64((count - i) % 17), Address: uint64(i + 1), Size: 16}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.runs) <= mergeFanIn {
		t.Fatal("fixture did not create multiple merge passes")
	}
	n := 0
	var last nativeRecord
	if err := s.each(func(r nativeRecord) error {
		if n > 0 && !lessNative(last, r) {
			t.Fatalf("unstable timestamp ordering: %#v then %#v", last, r)
		}
		last = r
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != count {
		t.Fatalf("lost records: %d", n)
	}
	s.close()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("scratch not removed: %v %v", files, err)
	}
}
func TestNativeSortVisitorFailureStillCleansAllRuns(t *testing.T) {
	dir := t.TempDir()
	s := nativeSorter{dir: dir, runItems: 8}
	for i := 0; i < 100; i++ {
		if err := s.push(nativeRecord{Time: uint64(i), Address: uint64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	want := errors.New("output failure")
	if err := s.each(func(nativeRecord) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	s.close()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("scratch after failure: %v %v", files, err)
	}
}
