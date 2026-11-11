# jfrconv-go

Native Go converter **0.1.0** targeting async-profiler **4.1**, with the original
flamegraph and heatmap interfaces, Span API filters and differential flame
graphs. Conversion runs entirely in Go with `CGO_ENABLED=0`; it needs no JVM,
Rust binary, Node.js or external Go modules at runtime.

## Build

Go 1.23 or newer is required. Linux and macOS are supported.

```sh
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/jfrconv ./cmd/jfrconv
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
./bin/jfrconv --version
```

Development verification uses Go 1.27.4; its official distribution URL and
verified SHA-256 are recorded in [reports/toolchain.json](reports/toolchain.json).
The module minimum remains Go 1.23.

## Convert

```sh
./bin/jfrconv --cpu recording.jfr profile.html
./bin/jfrconv --alloc --total recording.jfr allocations.collapsed
./bin/jfrconv --cpu -t -o pprof recording.jfr profile.pprof
./bin/jfrconv --cpu -o pb.gz recording.jfr profile.pb.gz
./bin/jfrconv --cpu -o otlp recording.jfr profile.otlp
./bin/jfrconv --cpu -o heatmap recording.jfr heatmap.html
./bin/jfrconv --nativemem --leak --total recording.jfr native-leaks.html
./bin/jfrconv --latency 100 --tag 'request-.*' recording.jfr requests.html
./bin/jfrconv --trace --total recording.jfr method-time.collapsed
./bin/jfrconv --diff base.collapsed current.collapsed difference.html
./bin/jfrconv -o collapsed profile.html recovered.collapsed
```

JFR supports HTML, collapsed stacks, pprof, gzip pprof (`pb.gz`), OTLP Profiles
protobuf and heatmap HTML. Collapsed stacks and compact async-profiler HTML
support HTML/collapsed conversion. `--diff` accepts two profiles and writes
HTML or collapsed output. Output format is inferred from the filename unless
`-o` is supplied; `-o heatmap` is needed for a heatmap ending in `.html`.

With one input and no output path, output goes to the working directory.
Multiple independent inputs require an existing output directory:

```sh
mkdir -p converted
./bin/jfrconv --cpu a.jfr b.jfr c.jfr converted/
```

As in Java 4.1, `--cpu` and `--wall` select the `STATE_DEFAULT` convention. For
a JDK recording with RUNNABLE execution samples, omit those switches. See
`--help` for thread/state filters, time bounds, line/BCI display, full-frame
regular expressions, reverse/inverted layouts and formatting options.

## Original UI and compatibility

The bundled flamegraph and heatmap HTML/CSS/JavaScript are the unchanged
async-profiler 4.1 templates. Heatmap ordering reproduces the reference's used
method IDs and deterministic RGB shades with bounded external sorting and a
capped LZ index. The diff UI uses the official newer upstream diff template.
See [UI-PARITY.md](UI-PARITY.md) for fixtures, exactness checks and specific
legacy-bug exceptions, and [COMPATIBILITY.md](COMPATIBILITY.md) for format and
test scope.

## Memory controls

```sh
./bin/jfrconv --memory-limit 64M --max-rss 128M --cpu input.jfr output.html
./bin/jfrconv --memory-stats a.jfr b.jfr c.jfr converted/
```

Sizes use binary units (`K`, `M`, `G`, `KiB`, `MiB`, `GiB`). The default has no
artificial memory ceiling. `--memory-limit` sets the Go runtime's **soft**
memory limit and a conservative application guard for major parser/model
stores. It does not account for every Go allocation and is not a hard heap or
RSS cap. The runtime can exceed its soft limit when required to make progress.

`--max-rss` runs conversion in a worker whose actual resident memory is checked
by a supervisor every 2 ms. It can briefly overshoot between samples, and the
supervisor's own memory is separate. An RSS guard also derives a conservative
application/runtime budget with 16 MiB reserved for overhead; an explicit
`--memory-limit` can lower it. Go's virtual-address reservations make a low
`RLIMIT_AS` unsuitable here. A strict kernel-accounted limit for the whole
process tree requires Linux cgroup v2 `memory.max` or a container memory limit.

A limit denial exits **75**, removes owned scratch files and leaves existing
regular output files intact. Limited workers stage outputs and publish only
after successful conversion. Ordinary outputs are replaced atomically per
file. Devices, FIFOs and descriptor paths such as `/dev/null` and `/dev/stdout`
receive the completed output through their existing handle; a stream cannot
provide regular-file atomic replacement.

`--memory-stats` reports post-GC `heap_alloc`, `heap_inuse`, `go_retained`, actual
`rss` and GC count after each file. It adds a diagnostic GC pause and should be
omitted from latency measurements. Runtime page retention can keep RSS above
the live heap. Memory still scales with the largest chunk's constant pools,
selected methods, distinct output prefixes, outstanding native allocations and
merged span intervals. [ARCHITECTURE.md](ARCHITECTURE.md) explains ownership and
spill bounds.

## Review and verification

Implementation began after three independent AI agents reviewed the design in
the roles of Go principal developers: memory/GC, compatibility/UI and
performance. All issued conditional approval; the accepted conditions and
measurement gates are recorded in [QUORUM.md](QUORUM.md). These are agent
reviews, not external human endorsements.

Tests cover parser bounds, Java golden outputs, span/method traces, all output
codecs, original JavaScript decoders, method-ID/RGB parity, forced external
spill/merge, native leak ordering, malformed input, memory denials, scratch
cleanup and publication preservation. Optional Node.js checks exercise the
original decoders; Java is only needed to rerun upstream/reference comparisons.
Public converter assertions are scoped and revision-pinned as described in
[COMPATIBILITY.md](COMPATIBILITY.md).

The final candidate passed **71/71 semantic checks** against Java 4.1. Its UI
corpus has 24 page pairs: 21 flamegraphs with identical HTML/CSS shells and,
at each of DPR 1 and 2, 273 identical interaction phases and 714 assertions;
three heatmap entries receive seeded copies only. Separate checks of ten
heatmap modes match used method IDs/RGB and decoded stack/timeline counts. See the
[final evidence](reports/releases/v0.1.0/evidence.json) and
[UI reports](UI-PARITY.md).

Native Linux arm64 execution passed **59 Go tests**, with two root-only
system-device probes explicitly skipped. The task-owned FIFO/descriptor tests
ran. A separate 64-input run completed under a strict 64 MiB whole-tree cgroup
with a recorded kernel memory peak of 17,154,048 bytes; this is cgroup memory
accounting, not process RSS. [Linux report](reports/linux/summary.json).

All **15 scoped published converter cases passed** with unchanged assertion
sources: eight historical 4.1 cases and six newer converter cases on macOS
under JDK 21, plus the historical Linux-only `NativememTests.ldpreload` on
Linux. The expected macOS skip for that Linux-only case is resolved by its
Linux pass. Earlier JDK 25 timing-fixture failures and same-recording Java/Go
replays are retained; see [COMPATIBILITY.md](COMPATIBILITY.md) for scope and
provenance. This does not claim the entire profiler test tree passed.

The final macOS arm64 matrix has **29 workloads, each with 21 measured pairs**
against Java 4.1. Go reduces median peak RSS in **29/29** rows by
**26.06–97.51%**, and also wins p95 peak RSS in all rows. It wins latency p50
in **25/29** rows and p95 in **26/29**. Four median-latency regressions remain:
10M events (**12.8% slower**), eight-input batch (**23.7%**), limited batch
(**25.8%**) and deep shared constant pools (**45.3%**). These are measured
workload-specific results, not a universal latency win. See
[BENCHMARKS.md](BENCHMARKS.md) for methods, raw trials and every row, and the
[final matrix summary](benchmarks/v0.1.0-macos/summary.json).

The measured Go executable SHA-256 is
`144204d7e755411903b058f5153aa583bd4adf4c1212667c52d73ae75c85bca6`;
the Java 4.1 reference JAR is
`2464b34abd47243941d81d53110591d66d7db3b3fae46c79fc85057eeddda19b`.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE); upstream attribution is
preserved in source and the original HTML assets.
