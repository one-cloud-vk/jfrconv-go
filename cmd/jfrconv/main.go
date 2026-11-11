// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"
	_ "time/tzdata" // Keep local calendar filters usable in minimal Linux images.

	"jfrconv-go/internal/cli"
	"jfrconv-go/internal/convert"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr, cli.Engine{Run: convert.Run, Diff: convert.RunDiff}))
}
