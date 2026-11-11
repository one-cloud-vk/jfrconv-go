// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"jfrconv-go/internal/convert"
)

const (
	envWorker     = "_JFRCONV_GO_WORKER"
	envBudget     = "_JFRCONV_GO_BUDGET"
	envRSS        = "_JFRCONV_GO_RSS"
	envOutputs    = "_JFRCONV_GO_OUTPUTS"
	envSpill      = "_JFRCONV_GO_SPILL"
	manifestName  = "manifest"
	manifestMagic = "JFRGO001"
)

type limitError struct{ message string }

func (e *limitError) Error() string { return e.message }

type workerSettings struct {
	budget, rss    uint64
	outputs, spill string
}

func configureWorker() (*workerSettings, error) {
	if os.Getenv(envWorker) != "1" {
		return nil, nil
	}
	budget, err := strconv.ParseUint(os.Getenv(envBudget), 10, 63)
	if err != nil || budget == 0 {
		return nil, fmt.Errorf("invalid worker memory budget")
	}
	rss, err := strconv.ParseUint(os.Getenv(envRSS), 10, 63)
	if err != nil {
		return nil, fmt.Errorf("invalid worker RSS guard")
	}
	settings := &workerSettings{budget: budget, rss: rss, outputs: os.Getenv(envOutputs), spill: os.Getenv(envSpill)}
	for _, directory := range []string{settings.outputs, settings.spill} {
		if !filepath.IsAbs(directory) {
			return nil, fmt.Errorf("invalid worker scratch directory")
		}
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("worker scratch directory is unavailable")
		}
	}
	debug.SetMemoryLimit(int64(budget))
	// Reject obviously unworkable budgets cleanly. SetMemoryLimit is otherwise
	// deliberately soft: no claim that every Go/runtime allocation is intercepted.
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if stats.Sys-stats.HeapReleased > budget {
		return nil, &limitError{fmt.Sprintf("memory budget is below worker startup memory (%d bytes)", budget)}
	}
	if rss != 0 {
		actual, err := residentBytes(os.Getpid())
		if err != nil {
			return nil, fmt.Errorf("cannot observe worker RSS: %w", err)
		}
		if actual > rss {
			return nil, &limitError{fmt.Sprintf("resident memory limit exceeded (%d > %d bytes)", actual, rss)}
		}
	}
	return settings, nil
}

func effectiveBudget(options convert.Options) uint64 {
	budget := options.MemoryLimit
	if options.MaxRSS != 0 {
		derived := uint64(1 << 20)
		if options.MaxRSS > 17<<20 {
			derived = options.MaxRSS - (16 << 20)
		}
		if budget == 0 || derived < budget {
			budget = derived
		}
	}
	return budget
}

func supervise(plan plan, args []string, stdout, stderr io.Writer) (int, error) {
	options := plan.config.Options
	if options.MaxRSS != 0 {
		if _, err := residentBytes(os.Getpid()); err != nil {
			return 1, fmt.Errorf("cannot observe RSS; refusing an unenforced guard: %w", err)
		}
		if options.MaxRSS < 1<<20 {
			return LimitExit, &limitError{"RSS limit is below the worker startup floor"}
		}
	}
	parent := filepath.Dir(plan.target)
	if info, err := os.Stat(plan.target); err == nil && info.IsDir() {
		parent = plan.target
	}
	if streamOutput(plan.target) {
		parent = ""
	}
	outputs, err := os.MkdirTemp(parent, ".jfrconv-worker-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(outputs)
	outputs, err = filepath.Abs(outputs)
	if err != nil {
		return 1, err
	}
	spill, err := os.MkdirTemp("", "jfrconv-spill-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(spill)
	spill, err = filepath.Abs(spill)
	if err != nil {
		return 1, err
	}
	executable, err := os.Executable()
	if err != nil {
		return 1, err
	}
	budget := effectiveBudget(options)
	command := exec.Command(executable, args...)
	command.Stdout, command.Stderr, command.Stdin = stdout, stderr, os.Stdin
	values := map[string]string{envWorker: "1", envBudget: strconv.FormatUint(budget, 10), envRSS: strconv.FormatUint(options.MaxRSS, 10), envOutputs: outputs, envSpill: spill, "TMPDIR": spill, "TMP": spill, "TEMP": spill, "GOMEMLIMIT": strconv.FormatUint(budget, 10) + "B"}
	command.Env = replaceEnvironment(os.Environ(), values)
	if err = command.Start(); err != nil {
		return 1, err
	}
	var waitErr error
	if options.MaxRSS == 0 {
		waitErr = command.Wait()
	} else {
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		missing := 0
		finished := false
		for !finished {
			select {
			case waitErr = <-done:
				finished = true
			case <-ticker.C:
				actual, err := residentBytes(command.Process.Pid)
				if err != nil {
					missing++
					if missing < 4 {
						continue
					}
					_ = command.Process.Kill()
					<-done
					return 1, fmt.Errorf("cannot sample live worker RSS; refusing an unenforced guard: %w", err)
				}
				missing = 0
				if actual > options.MaxRSS {
					_ = command.Process.Kill()
					<-done
					return LimitExit, &limitError{fmt.Sprintf("resident memory limit exceeded (%d > %d bytes)", actual, options.MaxRSS)}
				}
			}
		}
	}
	if waitErr != nil {
		if failure, ok := waitErr.(*exec.ExitError); ok {
			code := failure.ExitCode()
			if code < 0 {
				code = 1
			}
			return code, nil
		}
		return 1, waitErr
	}
	stats, err := readManifest(outputs, plan.jobs)
	if err != nil {
		return 1, err
	}
	for i, job := range plan.jobs {
		if err := publishFile(filepath.Join(outputs, stagedName(i)), job.output); err != nil {
			return 1, err
		}
		converted(stderr, job, stats[i])
	}
	return 0, nil
}

func replaceEnvironment(environment []string, values map[string]string) []string {
	result := make([]string, 0, len(environment)+len(values))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := values[key]; !replace {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func stagedName(id int) string { return "result-" + strconv.Itoa(id) }

func writeManifest(directory string, jobs []job, stats []convert.Stats) error {
	file, err := os.CreateTemp(directory, ".manifest-")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if _, err = io.WriteString(file, manifestMagic); err != nil {
		return err
	}
	if err = binary.Write(file, binary.LittleEndian, uint64(len(jobs))); err != nil {
		return err
	}
	for i, job := range jobs {
		for _, n := range []uint64{uint64(i), uint64(len(job.output)), stats[i].Samples, stats[i].Units} {
			if err = binary.Write(file, binary.LittleEndian, n); err != nil {
				return err
			}
		}
		if _, err = io.WriteString(file, job.output); err != nil {
			return err
		}
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, manifestName))
}

func readManifest(directory string, jobs []job) ([]convert.Stats, error) {
	file, err := os.Open(filepath.Join(directory, manifestName))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var magic [8]byte
	if _, err = io.ReadFull(file, magic[:]); err != nil || string(magic[:]) != manifestMagic {
		return nil, fmt.Errorf("invalid worker output manifest")
	}
	var count uint64
	if err = binary.Read(file, binary.LittleEndian, &count); err != nil || count != uint64(len(jobs)) {
		return nil, fmt.Errorf("incomplete worker output manifest")
	}
	stats := make([]convert.Stats, len(jobs))
	for i, job := range jobs {
		var header [4]uint64
		if err = binary.Read(file, binary.LittleEndian, &header); err != nil {
			return nil, err
		}
		if header[0] != uint64(i) || header[1] != uint64(len(job.output)) {
			return nil, fmt.Errorf("unauthorized worker output manifest entry")
		}
		path := make([]byte, len(job.output))
		if _, err = io.ReadFull(file, path); err != nil {
			return nil, err
		}
		if string(path) != job.output {
			return nil, fmt.Errorf("unauthorized worker output destination")
		}
		info, err := os.Lstat(filepath.Join(directory, stagedName(i)))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("worker staged output is not a regular file")
		}
		if err = validateDestination(job.output); err != nil {
			return nil, err
		}
		stats[i] = convert.Stats{Samples: header[2], Units: header[3]}
	}
	var extra [1]byte
	if n, err := file.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("worker output manifest has trailing data")
	}
	return stats, nil
}
