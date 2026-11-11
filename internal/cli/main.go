// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"jfrconv-go/internal/convert"
)

const LimitExit = 75

type Engine struct {
	Run  convert.Runner
	Diff func(base, current string, out io.Writer, format string, options convert.Options) (convert.Stats, error)
}

// Main owns CLI diagnostics and exit status. Engine injection keeps CLI and
// filesystem tests independent of the profile parser without shipping stubs.
func Main(args []string, stdout, stderr io.Writer, engine Engine) int {
	worker, err := configureWorker()
	if err != nil {
		return reportError(stderr, err)
	}
	config, err := Parse(args)
	if err != nil {
		return reportError(stderr, err)
	}
	if config.Version {
		fmt.Fprintf(stdout, "jfrconv-go %s (async-profiler 4.1 compatibility target; span extensions)\n", convert.Version)
		return 0
	}
	if config.Help || len(config.Files) == 0 {
		fmt.Fprint(stdout, Help)
		return 0
	}
	plan, err := makePlan(config)
	if err != nil {
		return reportError(stderr, err)
	}
	if worker == nil && (config.Options.MemoryLimit != 0 || config.Options.MaxRSS != 0) {
		code, err := supervise(plan, args, stdout, stderr)
		if err != nil {
			return reportError(stderr, err)
		}
		return code
	}
	if worker != nil {
		plan.config.Options.MemoryLimit = worker.budget
		plan.config.Options.TempDir = worker.spill
	}
	if config.Diff && engine.Diff == nil || !config.Diff && engine.Run == nil {
		return reportError(stderr, fmt.Errorf("conversion engine is unavailable"))
	}
	stats := make([]convert.Stats, 0, len(plan.jobs))
	for id, job := range plan.jobs {
		var outputDir string
		if worker != nil {
			outputDir = worker.outputs
		}
		stat, err := executeJob(job, plan.format, plan.config.Options, engine, outputDir, id)
		if err != nil {
			return reportError(stderr, err)
		}
		stats = append(stats, stat)
		if worker == nil {
			converted(stderr, job, stat)
		}
		if config.Options.MemoryStats {
			memoryStats(stderr)
		}
		// A limited multi-input invocation promptly releases the previous file's
		// garbage/pages before constructing another profile. A final single-file
		// conversion exits naturally, avoiding an unnecessary full GC on its path.
		if worker != nil && id+1 < len(plan.jobs) {
			debug.FreeOSMemory()
		}
	}
	if worker != nil {
		if err := writeManifest(worker.outputs, plan.jobs, stats); err != nil {
			return reportError(stderr, err)
		}
	}
	return 0
}

func reportError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "jfrconv: %v\n", err)
	if errors.Is(err, convert.ErrMemoryBudget) {
		return LimitExit
	}
	var limited *limitError
	if errors.As(err, &limited) {
		return LimitExit
	}
	return 1
}

func converted(stderr io.Writer, job job, stats convert.Stats) {
	fmt.Fprintf(stderr, "Converted %s -> %s (%d samples/units)\n", job.input, job.output, stats.Units)
}

func executeJob(job job, format string, options convert.Options, engine Engine, workerDir string, id int) (convert.Stats, error) {
	var stats convert.Stats
	temp, err := createOutputTemp(job.output, workerDir)
	if err != nil {
		return stats, err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	buffered := bufio.NewWriterSize(temp, 128*1024)
	if job.base != "" {
		stats, err = engine.Diff(job.base, job.input, buffered, format, options)
	} else {
		stats, err = engine.Run(job.input, buffered, format, options)
	}
	if err != nil {
		return stats, err
	}
	if err = buffered.Flush(); err != nil {
		return stats, err
	}
	if err = temp.Close(); err != nil {
		return stats, err
	}
	if workerDir != "" {
		err = os.Rename(temp.Name(), filepath.Join(workerDir, stagedName(id)))
	} else {
		err = publishFile(temp.Name(), job.output)
	}
	return stats, err
}

func memoryStats(stderr io.Writer) {
	// Explicit diagnostics report post-GC live heap. This pause is opt-in and
	// does not contaminate default conversion timing measurements.
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	rss, err := residentBytes(os.Getpid())
	if err != nil {
		rss = 0
	}
	fmt.Fprintf(stderr, "memory: heap_alloc=%d heap_inuse=%d go_retained=%d rss=%d gc=%d\n", stats.HeapAlloc, stats.HeapInuse, stats.Sys-stats.HeapReleased, rss, stats.NumGC)
}
