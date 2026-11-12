// SPDX-License-Identifier: Apache-2.0
package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTMLTitleInheritanceAndUnifiedDiffTemplate(t *testing.T) {
	dir := t.TempDir()
	collapsed := filepath.Join(dir, "input.collapsed")
	if err := os.WriteFile(collapsed, []byte("Caller_[j];Worker_[i] 7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	o := DefaultOptions()
	o.Title, o.HasTitle = "Request <b>latency</b>", true
	var first bytes.Buffer
	if _, err := Run(collapsed, &first, "html", o); err != nil {
		t.Fatal(err)
	}
	html := filepath.Join(dir, "original.html")
	if err := os.WriteFile(html, first.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, title, want string
		explicit          bool
	}{{"inherited", "Flame Graph", o.Title, false}, {"override", "Other", "Other", true}, {"explicit default", "Flame Graph", "Flame Graph", true}, {"empty", "", "", true}} {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultOptions()
			options.Title, options.HasTitle = test.title, test.explicit
			var output bytes.Buffer
			if _, err := Run(html, &output, "html", options); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "<h1>"+test.want+"</h1>") {
				t.Fatal("incorrect title")
			}
			if !strings.Contains(output.String(), "const maxdiff = -1;") {
				t.Fatal("normal graph did not use the 4.5 template")
			}
		})
	}
	var diff bytes.Buffer
	if _, err := RunDiff(collapsed, collapsed, &diff, "html", DefaultOptions()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.String(), "const maxdiff = 0;") || strings.Contains(diff.String(), "id='treeview'") {
		t.Fatal("differential graph did not use the unified 4.5 template")
	}
}
