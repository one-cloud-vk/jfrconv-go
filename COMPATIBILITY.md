# Compatibility and test scope

Version **0.2.0** targets the exact async-profiler **v4.5** tag, commit
`11aaea310cb75843f0e7532365d8cedc8d0e14c8`. Conversion is native Go, with no
runtime Java, Rust, Node.js or cgo dependency. This is a converter; it does not
implement the profiler agent or the entire async-profiler test tree.

The [release evidence](reports/releases/v0.2.0/evidence.json) pins the native
executable, pristine Java reference jar and source manifests. The original
build/Linux snapshot and final two-helper amendment are retained separately;
production and Go test source did not change after the build.

## Inputs and outputs

| Input | Outputs |
|---|---|
| JFR | HTML, collapsed, pprof, gzip pprof (`pb.gz`), OTLP Profiles protobuf, heatmap HTML |
| Collapsed stacks | HTML, collapsed |
| Compact async-profiler flamegraph HTML | HTML, collapsed |
| Two profiles with `--diff` | Differential HTML, base/current collapsed counts |

`-o pb` aliases pprof and `-o gz` aliases gzip pprof. Normal and differential
HTML use the same unchanged **4.5** flamegraph template; heatmaps use the
unchanged **4.5** heatmap template. Existing compact HTML is decoded without
executing its scripts. Source titles survive HTML conversion unless `--title`
is supplied; an explicitly empty title remains empty.

CPU/wall samples, `jdk.CPUTimeSample` (`--cpu-time`), allocations, live objects,
Java locks/parks, `profiler.NativeLock` (`--nativelock`) and native malloc/free
events follow 4.5 selection and weighting. `--total` selects bytes or duration
instead of counts. `--cpu`/`--wall` retain the `STATE_DEFAULT` convention; omit
them for ordinary JDK RUNNABLE execution samples. Weighted wall heatmaps place
repeated samples using the recorded wall/interval setting, with the reference
20 ms block and first-accepted-event origin rules. The raw optional `timeSpan`
field is consumed but does not replace that 4.5 interval rule.

Pprof has one selected sample value, with optional thread/category labels.
OTLP uses the **4.5 Profiles dictionary schema**, one selected value type and
one profile per ordinary recording chunk; native leak mode adds a final
retained-allocation profile. It preserves individual event timestamps and
groups samples by raw thread/stack identity, even without `--threads`.
Timestamps are packed fixed64; the values field is omitted when every selected
value in that chunk is one. The recorded settings determine the period. Empty
stacks remain valid protobuf samples. Encoded protobuf bytes, dictionary IDs
and compression can differ when decoded profile semantics agree.

`--diff` is part of the 4.5 target. Inputs are normalized for comparison;
collapsed diff output records base/current counts. The original differential
markers and unified upstream UI are preserved.

## Official Span API and method traces

`profiler.Span` and `jdk.MethodTrace` provide the intervals used by `--tag` and
`--latency`. Matching intervals are merged per thread; samples must fall in
`[start, end)`. Zero-duration intervals select no samples. Cross-chunk spans
and null versus empty tags are handled explicitly.

`--tag` is **literal equality**, including an empty string; `literal.*` is not
a regular expression. `--latency` accepts integer `s`, `ms`, `us` and `ns`
units, defaulting to milliseconds when no unit is given. Its default is
unfiltered (`-1`); `0ns` enables interval filtering. Thresholds are converted
to recording ticks before comparison, including non-1-GHz clocks.

`--trace` selects **only `jdk.MethodTrace`**. It neither converts Span events
directly nor appends a synthetic method leaf. The earlier
`profiler.MethodTrace` extension is not a 4.5 event alias. Pinned 4.5 has a
clock-unit bug: trace totals stay in raw ticks despite the nanoseconds label.
Go preserves that behavior; it does not silently use the later 4.6 correction.
[Pristine Java trace goldens](reports/releases/v0.2.0/official-span/methodtrace-reference.json).

Official API recordings cover start/end, endIfProfiled, emit/emitIfProfiled,
nesting, threads, cross-chunk spans, literal/empty/null tags and zero duration.
The JNI API writes modified UTF-8. Java 4.5's decoder replaces encoded surrogate
triples and overlong NUL bytes in a specific way; Go reproduces that behavior
for literal matching, rather than collapsing adjacent malformed sequences.
Valid UTF-8 retains its direct arena decoding path. The
[final official API comparison](reports/releases/v0.2.0/official-span/final-oracles.json)
contains **60 passing comparisons**: 21 Span cases, six Unicode tag/mode cases
across all six formats (36 comparisons), and three MethodTrace goldens.
Sixteen additional malformed-UTF-8 vectors are pinned to JDK 21 output.

## Specific boundaries

Go's RE2-based regular expressions support Java `\Q…\E` quoting and whole-frame
matching. Unsupported lookaround and backreferences return an explicit error.
`--title` accepts trusted raw HTML, as the Java converter does. Local-time
fractions are literal milliseconds: `.5` is 5 ms. Ambiguous/nonexistent local
wall times are rejected; epoch milliseconds are unambiguous. Negative time
bounds retain the progressively observed recording-end convention.

Heatmap used method IDs, original-renderer RGB and decoded timelines match the
reference on the recorded cases. Sparse dictionary ordering includes the
4.5 high-bit key remapping, unused constants, capacity across clears, typed
thread/class/method roles and stable compression ties. The 30-bit browser
format limits are checked explicitly. Compressed bytes need not be identical.
[UI-PARITY.md](UI-PARITY.md) gives the precise tested scope.

Go keeps retained native allocation paths resolved in their owning chunk.
Java 4.5 replays retained raw IDs against the final chunk, which can select an
unrelated reused stack or thread. That specific corruption is not reproduced;
leak grouping and grain rounding can therefore differ on cross-chunk ID reuse.
The older 4.1 extra-stack heatmap cache exceptions belong to the historical
0.1.x reports: 4.5 constructs those typed extra frames differently. There is
no general exception allowing ordinary heatmap IDs or shades to vary.

Same-named flamegraph siblings use Java integer-hash bucket ordering. Rare
treeified `HashMap` bucket reordering and an existing-key resize exactly at the
capacity threshold are not emulated; sibling position can differ there.
Flamegraph shades use upstream `Math.random()` and vary between loads.
Browser layout and font rasterization remain environment-dependent.

Malformed/truncated data and unsupported encodings return errors. Independent
inputs require an existing output directory. Regular outputs are published
after conversion succeeds; limits and stream publication are described in
[README.md](README.md) and [ARCHITECTURE.md](ARCHITECTURE.md).

## Verification and provenance

Fresh macOS checks passed **86 native tests with zero skips**, format, vet,
release build and **six Python codec tests**. The
[semantic comparison](reports/releases/v0.2.0/validation.json) passed **81/81**
checks against Java 4.5, including real JDK, async-profiler and official Span
API recordings, protobuf/HTML codecs, filters, malformed input and multi-file
publication. [Versioned evidence](reports/releases/v0.2.0/evidence.json).

The published converter-relevant scope contains **27 declarations at the exact
4.5 tag**. Its assertion sources are hash-verified before and after execution;
only converter transport entry points are replaced. On Linux **27/27 passed
with zero skips**. On macOS **19 passed and eight Linux-only configurations
skipped**, with no failures. A required native latency differential supplements
the upstream anonymous Java-visitor assertion, so that test also exercises
native filtering. These counts describe the pinned scope, not all profiler
tests. [Published results](reports/releases/v0.2.0/public/summary.json),
[Linux result](reports/releases/v0.2.0/linux/summary.json).

Linux arm64 passed **84 native tests**; two root-only device probes skipped.
Six Python codec tests and ten additional actual Span API differentials passed.
A 64-input run completed in a strict 64 MiB cgroup with kernel `memory.peak`
**16,977,920 bytes**, no OOM, and no scratch files left behind. That is charged
process-tree/page-cache memory, not process RSS or a universal workload bound.
[Linux evidence](reports/releases/v0.2.0/linux/summary.json).

Two test helpers were finalized after the native build/Linux run. The renderer
host gained the actual 4.5 dark-mode/legend DOM APIs and extra control checks;
the semantic validator's wall-heatmap case now uses the shipped 4.5 fixture
with ActiveSetting. Initial Java fixture failure (80/81) and missing-DOM host
failure remain in the reports. Native production/test source and executable
were unchanged. The amended helpers were verified on macOS; they were **not**
executed in the recorded Linux run.
[Exact amendment](reports/releases/v0.2.0/helper-amendment.json).

```sh
python3 tools/validate.py --go bin/jfrconv \
  --java /path/to/jdk21/bin/java \
  --java-jar /path/to/async-profiler-4.5/build/jar/jfr-converter.jar \
  --real recording.jfr
```

The original [0.1.1 evidence](reports/releases/v0.1.1/evidence.json) and
[0.1.0 evidence](reports/releases/v0.1.0/evidence.json) remain labeled with
their 4.1 reference, source, runtime and binary hashes. Their public outcomes,
JDK 25 workload-window failures, sandbox attach diagnostics and performance
matrices are historical results. They are not relabeled as 0.2.0 checks or
measurements. Current and historical measurement scopes are recorded in
[BENCHMARKS.md](BENCHMARKS.md); functional equivalence does not establish a
latency or RSS ratio.
