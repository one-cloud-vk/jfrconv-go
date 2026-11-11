# Compatibility and test scope

The core reference is async-profiler tag **v4.1**, commit
`5930966a92860f6e5d2d89ab6faab5815720bad9`. This project implements the
converter in native Go. It does not implement the profiler agent, JVM
integration or the complete async-profiler test tree.

## Inputs, outputs and extensions

| Input | Outputs |
|---|---|
| JFR | HTML, collapsed, pprof, gzip pprof (`pb.gz`), OTLP Profiles protobuf, heatmap HTML |
| Collapsed stacks | HTML, collapsed |
| Compact async-profiler flamegraph HTML | HTML, collapsed |
| Two profiles with `--diff` | Differential HTML, base/current collapsed counts |

`-o pb` aliases pprof and `-o gz` aliases gzip pprof. OTLP uses the Profiles
protobuf schema bundled with 4.1. The original HTML templates and decoders are
embedded in the executable. Existing compact HTML can be read back without
running its scripts.

CPU/wall samples, allocations, live objects, locks/parks and native malloc/free
events retain the reference selection and weighting conventions. `--total`
sums bytes or duration instead of sample counts. Filters cover thread/state,
time range and full-frame regular expressions. `--cpu`/`--wall` retain the
4.1 `STATE_DEFAULT` convention; omit them for ordinary JDK RUNNABLE execution
samples. Negative time bounds retain the progressively observed recording-end
behavior of 4.1. Local time fractions are literal milliseconds: `.5` is 5 ms.
Ambiguous/nonexistent local wall times are rejected; use epoch milliseconds.

Span API support is an extension: `profiler.Span`, `profiler.MethodTrace` and
`jdk.MethodTrace` supply span/method intervals. `--latency` and `--tag` select
samples inside matching same-thread intervals; intervals are merged before the
filtering pass. `--trace` converts span/method durations directly. `--total`
then reports their duration.

`--diff` is also an extension. Its unchanged upstream UI template comes from
public commit `e98d8c2b2d6e954ed1b39685fed1b25dc08db603`. HTML retains the
original differential markers; collapsed output includes base/current counts.
Names are normalized for comparison and the current profile can be recovered
from differential data.

## Specific compatibility boundaries

Go's RE2-based regular expressions support Java `\Q…\E` quoting and whole-frame
matching. Unsupported lookaround and backreferences return an explicit error.
The CLI accepts trusted raw `--title` HTML, matching Java 4.1 behavior.

Heatmap used method IDs and original-renderer RGB match the reference ordering
on the tested reference-defined inputs. The compressed byte stream can differ
while decoding to identical methods, IDs, blocks, counts and locations. The
30-bit limits of the original browser format are checked explicitly. See
[UI-PARITY.md](UI-PARITY.md) for exactness tests and fixtures.

Known Java 4.1 identity bugs remain corrected. Its extra-stack cache aliases
reused chunk-local IDs and omits the TLAB/outside-TLAB distinction; this can
assign the wrong method or frame kind in allocation/lock heatmaps. Its native
leak grouping can also alias raw stack IDs reused across chunks. Go preserves
distinct typed identities and resolves retained allocation paths before the
owning chunk disappears. These cases can yield a different, corrected graph;
leak grain rounding can differ from Java's raw-ID grouping. They are explicit
legacy incorrect-alias exceptions, not a general waiver for heatmap shades.

Same-named flamegraph siblings use Java integer-hash bucket ordering. Rare
treeified `HashMap` bucket reordering and an existing-key compute resize
exactly at the capacity threshold are not emulated. These can affect sibling
position. Production flamegraph shades use upstream `Math.random()` and vary
between loads. Browser layout/font differences also prevent a universal
pixel-equality guarantee.

Malformed/truncated data and unsupported encodings return errors. Multiple
independent inputs require an existing output directory. Regular output files
are published only after conversion succeeds; memory limits and stream output
semantics are documented in [README.md](README.md).

## Verification and provenance

Native tests include parser bounds, recorded Java outputs, weighted samples,
cross-chunk identities, native leaks, span/tag filters, method traces,
empty-stack protobuf locations, OTLP hierarchy/IDs, original HTML decoding,
diff reconstruction, malformed input and memory denial cleanup. Heatmap tests
include unused constant-pool stacks, dictionary capacity across clears,
frequency ties, locations, start roles, forced spill/merge and large weighted
repetition. Optional Node.js tests use the unchanged original decoder; they
are skipped explicitly if Node.js is unavailable.

The public-test adapter stages a pristine upstream revision, verifies SHA-256
hashes of test assertion sources and replaces only converter transport entry
points. The published Java assertion bodies run unchanged against the native
binary. The historical 4.1 scope consists of nine converter-consuming test
declarations, including Linux-only `NativememTests.ldpreload`. The newer public
converter suite is pinned separately at
`e98d8c2b2d6e954ed1b39685fed1b25dc08db603` and contains six converter cases.
Version **0.1.1** passed all **15 scoped published converter cases**: eight
historical 4.1 cases and six newer converter cases under JDK 21.0.7 on macOS,
plus native Linux `NativememTests.ldpreload`. The expected historical macOS
`SKIP_CONFIG_MISMATCH` is that Linux-only declaration, resolved by its Linux
pass. Assertion sources were verified unchanged before and after execution.
[Consolidated public results](reports/releases/v0.1.1/public/summary.json),
[macOS runs](reports/releases/v0.1.1/public/success/summary.json),
[Linux result](reports/releases/v0.1.1/linux/summary.json).
This scope does not mean the entire profiler test tree was run.

The first two 0.1.1 macOS attempts used a restricted execution sandbox and
failed before conversion because the native profiler could not start the
JVM's attach socket. A separate owned-JVM probe confirmed the target was alive
before and after that failure; a local socket-bind probe returned `EPERM`.
The successful full attempt used the same permitted attach context as the
previous baseline, without sudo or root. Assertions and converter source did
not change between attempts. Both setup failures and the live-target evidence
are retained: [attempt 1](reports/releases/v0.1.1/public/prior-default-sandbox/attempt-1/summary.json),
[attempt 2](reports/releases/v0.1.1/public/prior-default-sandbox/attempt-2/summary.json),
[diagnostic](reports/releases/v0.1.1/public/attach-diagnostic-default/report.json).

Fresh 0.1.1 checks passed **65 macOS tests with no skips**, format, vet and
release build, plus [71/71 semantic comparisons](reports/releases/v0.1.1/validation.json)
against Java 4.1. They cover real JDK/async-profiler recordings, protobuf and
HTML codecs, span filters, malformed-input preservation, compact stack columns
with full-width method IDs/locations and reusable path scratch ownership.
The [versioned evidence](reports/releases/v0.1.1/evidence.json) pins the frozen
source and executable. [UI-PARITY.md](UI-PARITY.md) records fresh renderer and
heatmap ID/RGB checks.

On Linux arm64, **63 tests passed and two root-only system-device tests
skipped**; task-owned FIFO/descriptor tests and the original JavaScript color
oracle ran. A 64-input conversion completed in a strict 64 MiB whole-tree
cgroup with kernel `memory.peak` **17,063,936 bytes** and no OOM events. This
includes process-tree and charged page-cache memory, not just process RSS.
[Linux report](reports/releases/v0.1.1/linux/summary.json).

The [0.1.0 evidence](reports/releases/v0.1.0/evidence.json) and
[public history](reports/public-macos/v0.1.0/summary.json) remain unchanged.
That earlier release had two JDK 25 `regularPeak` workload-window failures;
Java 4.1 and Go decoded each retained recording identically, and the subsequent
native JDK 21 run passed unchanged assertions. Those failures are separate
from 0.1.1's pre-conversion sandbox setup failures. Historical Java baseline
reports retain their original runtime/date; they are not presented as fresh
Java baseline runs. The older Linux benchmark-input-pool helper amendment is
also retained with its [original provenance](reports/linux/benchmark-input-pool-amendment.json).

```sh
# Historical published assertion sources; use a fresh destination.
python3 tools/upstream_tests.py stage --source /path/to/async-profiler \
  --ref v4.1 --dest work/public-v41 --suite v4.1 --bridge
JFRCONV_BINARY="$PWD/bin/jfrconv" \
  python3 tools/upstream_tests.py run --dest work/public-v41 --suite v4.1

# The separately pinned current public converter cases.
python3 tools/upstream_tests.py stage --source /path/to/async-profiler \
  --ref e98d8c2b2d6e954ed1b39685fed1b25dc08db603 \
  --dest work/public-current --suite current --bridge
JFRCONV_BINARY="$PWD/bin/jfrconv" \
  python3 tools/upstream_tests.py run --dest work/public-current --suite current

# Semantic comparisons against the original Java 4.1 converter.
python3 tools/validate.py --go bin/jfrconv \
  --java-jar /path/to/v4.1/build/jar/jfr-converter.jar
```

Upstream runs require a JDK and native profiler build prerequisites; the full
historical scope requires Linux.

The historical **0.1.0** Java-paired matrix has **29 workloads with 21 measured
pairs each**, pinned to that release's executable. Median peak RSS is lower in
29/29 rows (26.06–97.51% reduction), as is p95 peak RSS; p50 latency wins 25/29
and p95 wins 26/29. These figures remain labeled 0.1.0. Version 0.1.1 has a
separate six-workload old/new followup with its own frozen binary and source
provenance. [BENCHMARKS.md](BENCHMARKS.md) records the methods, raw trials and
regressions for each version; historical results are not relabeled as 0.1.1.
[Historical matrix](benchmarks/v0.1.0-macos/summary.json).
