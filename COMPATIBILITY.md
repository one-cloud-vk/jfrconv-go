# Compatibility

The reference is async-profiler **v4.5**, commit
`11aaea310cb75843f0e7532365d8cedc8d0e14c8`.
This project implements the converter, not the profiler agent.

## Formats and events

| Input | Output |
|---|---|
| JFR | HTML, collapsed, pprof, gzip pprof (`pb.gz`), OTLP, heatmap HTML |
| Collapsed stacks | HTML, collapsed |
| Compact async-profiler flamegraph HTML | HTML, collapsed |
| Two profiles with `--diff` | Differential HTML or base/current collapsed counts |

`-o pb` aliases pprof; `-o gz` aliases gzip pprof. HTML input is decoded without
executing its scripts. Its title survives conversion unless `--title` is given;
an explicitly empty title remains empty.

Supported events include CPU/wall samples, CPU-time samples (`--cpu-time`),
allocations, live objects, Java locks/parks, native locks (`--nativelock`) and
native malloc/free. `--total` selects bytes or duration instead of counts.
As in Java 4.5, `--cpu` and `--wall` use async-profiler's `STATE_DEFAULT`
convention; omit them for ordinary JDK RUNNABLE execution samples.

Pprof contains the selected sample value, with optional thread/category labels.
OTLP uses the 4.5 Profiles Alpha schema (opentelemetry-proto 1.10), preserving
individual timestamps and grouping by raw thread/stack identity. It emits one
profile per ordinary recording chunk; native leak mode adds a retained-allocation
profile. Compression and protobuf dictionary IDs may differ while decoded
profile contents agree.

## Spans and method traces

`profiler.Span` and `jdk.MethodTrace` supply intervals for `--tag` and
`--latency`. Both filters must match the same interval on the sampled thread.
Matching intervals are merged per thread. Bounds are `[start, end)`;
zero-duration spans select no samples. Spans can cross recording chunks.

`--tag` uses literal equality, including an empty string. `--latency` accepts
integer `s`, `ms`, `us` and `ns`, defaulting to milliseconds. The default is
unfiltered; `0ns` enables interval filtering. Thresholds are converted to the
recording clock before comparison.

`--trace` selects only `jdk.MethodTrace`, without adding a synthetic method
frame. Trace totals retain Java 4.5's raw-tick behavior despite the nanoseconds
label. Modified UTF-8 tags from the JNI Span API use Java 4.5's replacement
behavior for malformed bytes.

## UI

The embedded flamegraph and heatmap templates are unchanged from 4.5.
Normal and differential flamegraphs share the upstream template. Heatmap
encoding reproduces Java's used method IDs, RGB shades and decoded timeline,
including thread frames, weighted wall samples and dictionary ordering.
Compressed bytes can differ.

Flamegraph colors use upstream `Math.random()` and vary between page loads.
Browser fonts and rasterization also depend on the environment. The renderer
tests compare the original scripts' draw calls and interaction behavior at
DPR 1 and 2; they are not browser screenshot tests.

## Known differences

- Go regular expressions support Java `\Q…\E` quoting and whole-frame matching.
  Lookaround and backreferences return an error.
- Retained native allocation paths stay associated with their original chunk.
  Java 4.5 can resolve reused stack/thread IDs against the final chunk instead.
  Leak grouping and grain rounding can differ on such recordings.
- Same-named flamegraph siblings follow Java integer-hash bucket ordering.
  Rare treeified buckets and existing-key resizes at the capacity threshold
  can produce a different sibling order.
- Ambiguous or nonexistent local wall times are rejected. Epoch milliseconds
  are unambiguous. Fractional local times are literal milliseconds: `.5` is 5 ms.

`--title` accepts trusted raw HTML, as in Java. Malformed/truncated recordings
and unsupported encodings return errors. See [TESTING.md](TESTING.md) for
reference comparisons and the public test scope.
