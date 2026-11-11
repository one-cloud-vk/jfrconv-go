// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type job struct{ input, base, output string }
type plan struct {
	config Config
	format string
	jobs   []job
	target string
}

func makePlan(c Config) (plan, error) {
	p := plan{config: c}
	implicit := 1
	if c.Diff {
		implicit = 2
	}
	if len(c.Files) < implicit {
		return p, fmt.Errorf("--diff requires two inputs")
	}
	inputs := c.Files
	p.target = "."
	if len(inputs) > implicit {
		p.target = inputs[len(inputs)-1]
		inputs = inputs[:len(inputs)-1]
	}
	info, err := os.Stat(p.target)
	directory := err == nil && info.IsDir()
	if err != nil && !os.IsNotExist(err) {
		return p, err
	}
	if len(inputs) > 1 && !directory && !c.Diff {
		return p, fmt.Errorf("for multiple inputs output must be an existing directory")
	}
	format := c.Output
	if format == "" {
		if directory {
			format = "html"
		} else {
			format = strings.TrimPrefix(filepath.Ext(p.target), ".")
			if format == "" {
				format = "html"
			}
		}
	}
	extension := format
	switch format {
	case "html", "collapsed", "otlp", "heatmap", "pprof", "pb.gz":
		p.format = format
	case "pb":
		p.format = "pprof"
	case "gz":
		p.format = "pb.gz"
		extension = "pb.gz"
	default:
		return p, fmt.Errorf("unknown output format %q", format)
	}
	if c.Diff && (len(inputs) != 2 || p.format != "html" && p.format != "collapsed") {
		return p, fmt.Errorf("--diff requires two inputs and html or collapsed output")
	}
	if p.format == "heatmap" {
		extension = "html"
	}
	if c.Diff {
		extension = "diff." + extension
		p.config.Options.Norm = true
	}
	base := ""
	if c.Diff {
		base = inputs[0]
		inputs = inputs[1:]
	}
	for _, input := range inputs {
		output := p.target
		if directory {
			name := filepath.Base(input)
			if name == "." || name == string(filepath.Separator) {
				return p, fmt.Errorf("invalid input filename")
			}
			ext := filepath.Ext(name)
			// Rust Path::with_extension and Java preserve a leading-only dot.
			if ext == name {
				ext = ""
			}
			output = filepath.Join(p.target, strings.TrimSuffix(name, ext)+"."+extension)
		}
		for _, source := range []string{input, base} {
			if source == "" {
				continue
			}
			if sameFile(source, output) {
				return p, fmt.Errorf("input and output must be different files")
			}
		}
		if err := validateDestination(output); err != nil {
			return p, err
		}
		p.jobs = append(p.jobs, job{input: input, base: base, output: output})
	}
	return p, nil
}

func sameFile(a, b string) bool {
	ai, ae := os.Stat(a)
	bi, be := os.Stat(b)
	return ae == nil && be == nil && os.SameFile(ai, bi)
}
