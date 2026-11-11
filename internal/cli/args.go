// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jfrconv-go/internal/convert"
)

const Help = `jfrconv-go — native converter targeting async-profiler 4.1
Usage: jfrconv [options] <input> [<input>...] [<output>]

  -o, --output FORMAT   html (default), collapsed, pprof, pb.gz, otlp, heatmap
      --cpu / --wall    Select CPU / wall-clock samples
      --alloc / --live  Select allocation / live object events
      --lock           Select monitor-enter and thread-park events
      --nativemem      Select native malloc/free events
      --leak           Retain outstanding native allocations only
      --tail RATIO     Ignore last fraction of leak allocations (default 0.1)
  -t, --threads        Group stacks by thread
  -s, --state LIST     Filter thread states (comma-separated prefixes)
      --total          Sum bytes/durations instead of sample counts
      --from TIME      Start: relative/epoch milliseconds or local date/time
      --to TIME        End: milliseconds or local HH:mm:ss / ISO date-time
      --lines / --bci  Include source line / bytecode index in flame graphs
      --simple         Short class names
      --norm           Normalize hidden class and lambda names
      --dot            Dotted class names
      --classify       Group stacks into JVM activity categories
  -I, --include REGEX   Keep stacks containing a full-matching frame
  -X, --exclude REGEX   Remove stacks containing a full-matching frame
      --title TEXT     Flame graph title (trusted HTML, as in Java 4.1)
      --minwidth N     Minimum frame width as percent of total
      --grain N        Coarsen samples by this factor
      --skip N         Skip N root frames
  -r, --reverse        Reverse stack direction, retaining thread grouping
  -i, --inverted       Toggle flame graph / icicle layout
      --highlight RE   Initial HTML search expression
      --diff           Compare two profiles (base and current)
      --memory-stats   Print post-GC heap/runtime memory and actual RSS
      --memory-limit N Soft Go runtime/application budget, e.g. 64M
      --max-rss N      Worker RSS guard (2 ms polling; can briefly overshoot)

Limits run in an isolated worker. Failure exits 75 and removes owned scratch;
staged outputs are published only after success. GOMEMLIMIT is soft and is
not a kernel RSS cap. Linux cgroups/container limits provide hard accounting.

Span extensions:
      --latency MS     Same-thread spans whose duration is at least MS
      --tag REGEX      Same-thread spans whose tag fully matches
      --trace          Convert profiler.Span / MethodTrace durations

      --version        Print version and compatibility target
  -h, --help           Show this help
`

type Config struct {
	Options             convert.Options
	Output              string
	Files               []string
	Diff, Help, Version bool
}

func Parse(args []string) (Config, error) {
	c := Config{Options: convert.DefaultOptions()}
	var alloc, live, lock, native, trace, literal bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if literal || !strings.HasPrefix(arg, "-") || arg == "-" {
			c.Files = append(c.Files, arg)
			continue
		}
		if arg == "--" {
			literal = true
			continue
		}
		value := func() (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			return args[i], nil
		}
		var err error
		var text string
		switch arg {
		case "-h", "--help":
			c.Help = true
		case "--version":
			c.Version = true
		case "--diff":
			c.Diff = true
		case "--cpu":
			c.Options.CPU = true
		case "--wall":
			c.Options.Wall = true
		case "--alloc":
			alloc = true
		case "--live":
			live = true
		case "--lock":
			lock = true
		case "--nativemem":
			native = true
		case "--trace":
			trace = true
		case "-t", "--threads":
			c.Options.Threads = true
		case "--total":
			c.Options.Total = true
		case "--lines":
			c.Options.Lines = true
		case "--bci":
			c.Options.BCI = true
		case "--simple":
			c.Options.Simple = true
		case "--norm":
			c.Options.Norm = true
		case "--dot":
			c.Options.Dot = true
		case "--classify":
			c.Options.Classify = true
		case "-r", "--reverse":
			c.Options.Reverse = true
		case "-i", "--inverted":
			c.Options.Inverted = true
		case "--leak":
			c.Options.Leak = true
		case "--memory-stats":
			c.Options.MemoryStats = true
		case "-o", "--output":
			c.Output, err = value()
		case "--title":
			c.Options.Title, err = value()
		case "--highlight":
			c.Options.Highlight, err = value()
			c.Options.HasHighlight = err == nil
		case "-s", "--state":
			text, err = value()
			if err == nil {
				c.Options.State = strings.Split(strings.ToUpper(text), ",")
			}
		case "-I", "--include", "-X", "--exclude", "--tag":
			text, err = value()
			if err == nil {
				var expression *regexp.Regexp
				expression, err = Pattern(text)
				if arg == "-I" || arg == "--include" {
					c.Options.Include = expression
				} else if arg == "-X" || arg == "--exclude" {
					c.Options.Exclude = expression
				} else {
					c.Options.Tag = expression
				}
			}
		case "--from", "--to":
			text, err = value()
			if err == nil {
				var stamp int64
				stamp, err = parseTimestamp(text, time.Now(), time.Local)
				if arg == "--from" {
					c.Options.From = stamp
				} else {
					c.Options.To = stamp
				}
			}
		case "--memory-limit", "--max-rss":
			text, err = value()
			if err == nil {
				var size uint64
				size, err = ParseSize(text)
				if arg == "--memory-limit" {
					c.Options.MemoryLimit = size
				} else {
					c.Options.MaxRSS = size
				}
			}
		case "--skip":
			text, err = value()
			if err == nil {
				var n uint64
				n, err = strconv.ParseUint(text, 10, strconv.IntSize)
				if err == nil && n > uint64(^uint(0)>>1) {
					err = fmt.Errorf("--skip is too large")
				}
				c.Options.Skip = int(n)
			}
		case "--minwidth", "--grain", "--tail", "--latency":
			text, err = value()
			if err == nil {
				var n float64
				n, err = nonnegative(text)
				switch arg {
				case "--minwidth":
					c.Options.MinWidth = n
				case "--grain":
					c.Options.Grain = n
				case "--tail":
					c.Options.Tail = n
				case "--latency":
					c.Options.Latency = n
					c.Options.HasLatency = err == nil
				}
			}
		default:
			return c, fmt.Errorf("unknown option %s; use --help", arg)
		}
		if err != nil {
			return c, fmt.Errorf("%s: %w", arg, err)
		}
	}
	switch {
	case native:
		c.Options.Mode = convert.ModeNative
	case live:
		c.Options.Mode = convert.ModeLive
	case alloc:
		c.Options.Mode = convert.ModeAllocation
	case lock:
		c.Options.Mode = convert.ModeLock
	case trace:
		c.Options.Mode = convert.ModeTrace
	}
	if c.Options.Tail > 1 {
		return c, fmt.Errorf("--tail must be between 0 and 1")
	}
	if c.Options.Leak && c.Options.Mode != convert.ModeNative {
		return c, fmt.Errorf("--leak requires --nativemem")
	}
	if c.Options.Mode != convert.ModeExecution && (c.Options.CPU || c.Options.Wall || c.Options.State != nil) {
		return c, fmt.Errorf("CPU/wall/thread-state filters require execution samples")
	}
	return c, nil
}

// Pattern uses full-frame matching, including RE2's Java-compatible \Q...\E.
// Unsupported lookaround/backreferences are rejected by regexp.Compile.
func Pattern(text string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(`\A(?:` + text + `)\z`)
	if err != nil {
		return nil, fmt.Errorf("unsupported or invalid filter regex: %w", err)
	}
	return re, nil
}

func nonnegative(text string) (float64, error) {
	percent := strings.HasSuffix(text, "%")
	if percent {
		text = strings.TrimSuffix(text, "%")
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, fmt.Errorf("expected a finite nonnegative number")
	}
	if percent {
		n /= 100
	}
	return n, nil
}

func ParseSize(text string) (uint64, error) {
	i := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid memory size %q", text)
	}
	n, err := strconv.ParseUint(text[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory size %q", text)
	}
	var multiplier uint64
	switch strings.ToLower(text[i:]) {
	case "", "b":
		multiplier = 1
	case "k", "kb", "kib":
		multiplier = 1 << 10
	case "m", "mb", "mib":
		multiplier = 1 << 20
	case "g", "gb", "gib":
		multiplier = 1 << 30
	default:
		return 0, fmt.Errorf("invalid memory size suffix in %q", text)
	}
	if n == 0 || n > uint64(math.MaxInt64)/multiplier {
		return 0, fmt.Errorf("memory limit must be positive and fit int64")
	}
	return n * multiplier, nil
}

func parseTimestamp(text string, now time.Time, location *time.Location) (int64, error) {
	if !strings.Contains(text, ":") {
		return strconv.ParseInt(text, 10, 64)
	}
	day := now.In(location)
	clock := text
	if date, tail, found := strings.Cut(text, "T"); found {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			return 0, fmt.Errorf("invalid date: %w", err)
		}
		day, clock = parsed, tail
	}
	parts := strings.Split(clock, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid local time")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	seconds := "0"
	if len(parts) == 3 {
		seconds = parts[2]
	}
	secondText, milliText, hasMillis := strings.Cut(seconds, ".")
	second, err := strconv.Atoi(secondText)
	if err != nil {
		return 0, err
	}
	var millis int64
	if hasMillis {
		millis, err = strconv.ParseInt(milliText, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return 0, fmt.Errorf("invalid local time")
	}
	t := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, location)
	sameWall := func(a time.Time) bool {
		a = a.In(location)
		return a.Year() == day.Year() && a.Month() == day.Month() && a.Day() == day.Day() && a.Hour() == hour && a.Minute() == minute && a.Second() == second
	}
	if !sameWall(t) {
		return 0, fmt.Errorf("nonexistent local date-time; use epoch milliseconds")
	}
	_, offset := t.Zone()
	for _, delta := range []time.Duration{-48 * time.Hour, -24 * time.Hour, 24 * time.Hour, 48 * time.Hour} {
		_, otherOffset := t.Add(delta).Zone()
		if otherOffset != offset && sameWall(t.Add(time.Duration(offset-otherOffset)*time.Second)) {
			return 0, fmt.Errorf("ambiguous local date-time; use epoch milliseconds")
		}
	}
	base := t.UnixMilli()
	if millis > 0 && base > math.MaxInt64-millis || millis < 0 && base < math.MinInt64-millis {
		return 0, fmt.Errorf("timestamp overflow")
	}
	return base + millis, nil
}
