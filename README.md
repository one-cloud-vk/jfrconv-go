# jfrconv-go

Native Go converter **0.2.0** targeting async-profiler **4.5**. It includes
flamegraphs, heatmaps, Span API filters and differential flamegraphs. The
executable needs no JVM, cgo or external Go modules.

## Build

Go 1.23+; Linux and macOS. Release verification uses Go 1.27.4.

```sh
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/jfrconv ./cmd/jfrconv
go test ./...
go vet ./...
```

## Convert

```sh
./bin/jfrconv --cpu recording.jfr profile.html
./bin/jfrconv --alloc --total recording.jfr allocations.collapsed
./bin/jfrconv --cpu -o pprof recording.jfr profile.pprof
./bin/jfrconv --cpu -o heatmap recording.jfr heatmap.html
./bin/jfrconv --cpu --latency 100ms --tag 'GET /api/orders' recording.jfr requests.html
./bin/jfrconv --diff base.collapsed current.collapsed difference.html
```

JFR output formats: `html`, `collapsed`, `pprof`, `pb.gz`, `otlp`, `heatmap`.
Compact async-profiler HTML and collapsed stacks support HTML/collapsed
conversion. `--diff` accepts two profiles and writes HTML or collapsed output.
Output format follows the filename unless `-o` is given; use `-o heatmap` for a
heatmap ending in `.html`. Use `--help` for filters, native leak analysis,
method traces, line/BCI display and layout options.

With one input and no output path, output goes to the working directory.
Multiple inputs require an existing output directory:

```sh
mkdir -p converted
./bin/jfrconv --cpu a.jfr b.jfr converted/
```

As in Java 4.5, `--cpu` selects async-profiler's `STATE_DEFAULT` convention.
For JDK RUNNABLE execution samples, omit `--cpu`/`--wall`.

## Span API

Record spans with the official async-profiler 4.5 Java API:

```java
long span = one.profiler.Span.start();
try {
    handleRequest();
} finally {
    one.profiler.Span.end(span, "GET /api/orders");
}
```

Convert the resulting JFR with `--latency 100ms --tag 'GET /api/orders'`.
Both conditions must match the same span on the sampled thread. Intervals are
`[start, end)`. `--tag` is an exact literal match in 4.5; regex matching from
older jfrconv-go extensions is no longer used. Durations accept integer
`s`, `ms`, `us` and `ns`, defaulting to milliseconds. `--trace` selects
`jdk.MethodTrace` events; it does not convert Span API spans into method traces.
`--cpu-time` and `--nativelock` select the additional 4.5 event families.

## Memory limits

```sh
./bin/jfrconv --memory-limit 64M --max-rss 128M --cpu input.jfr output.html
./bin/jfrconv --memory-stats a.jfr b.jfr converted/
```

Sizes use binary units. `--memory-limit` combines a conservative guard for
major parser/model stores with Go's **soft** runtime memory limit. It does
not cover every allocation and is not a hard RSS cap.

`--max-rss` supervises a separate worker, sampling actual resident memory every
2 ms. It can overshoot between samples; the supervisor's memory is separate.
Use Linux cgroup v2 `memory.max` or a container limit for a strict limit on the
whole process tree. An RSS guard reserves 16 MiB for runtime overhead when
deriving the worker's application budget.

Limit denial exits **75**, removes owned scratch and preserves existing
regular outputs. Limited workers finish the batch before publishing. Regular
file replacement is atomic per file. Devices, FIFOs and descriptor outputs
receive completed bytes through their existing handles.

`--memory-stats` forces a diagnostic GC after each input and reports heap,
runtime-retained memory and RSS. Omit it from latency measurements. Memory
scales with the largest chunk, distinct output prefixes, retained native
allocations and merged span intervals. See [ARCHITECTURE.md](ARCHITECTURE.md).

## Compatibility and measurements

The flamegraph and heatmap templates are the original async-profiler 4.5
HTML/CSS/JavaScript. Heatmap encoding preserves Java's used method IDs and RGB
shades. Normal and differential flamegraphs use the same official 4.5 template. See
[UI-PARITY.md](UI-PARITY.md) for checks and documented Java bug exceptions.

[COMPATIBILITY.md](COMPATIBILITY.md) describes public test scope and how to run
the Java/Node reference checks. [BENCHMARKS.md](BENCHMARKS.md) contains versioned
RSS/latency measurements and raw trials. Version 0.2.0 writes the OTLP Profiles
Alpha schema bundled with 4.5 (opentelemetry-proto 1.10); this replaces the old
4.1 OTLP format. Historical measurements retain their original versions and
binary hashes. Initial design reviews are recorded in [QUORUM.md](QUORUM.md).

## License

Apache-2.0; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
