# Benchmarks

These measurements compare Go 0.2.0 (`cd4461d`) with the original Java jfrconv
from async-profiler v4.5 (`11aaea310cb75843f0e7532365d8cedc8d0e14c8`).
Measured native SHA256: `a9d7e8fc1fb440157df5f56ae34029e78cdf5b79005bc38d7a9406da4df1feb5`.
The [raw results](benchmarks/results.json) retain all trials and input hashes.
Go used less median and p95 peak RSS in all nine cases. Latency improved in
four p50 and five p95 comparisons; OTLP uses substantially less memory but is
about 4.9 times slower at p50.

## Method

Measured on macOS 15.8.2 arm64, eight logical CPUs, using Go 1.27.4 and OpenJDK
21.0.7. The native binary used `CGO_ENABLED=0`, `-trimpath` and `-ldflags='-s -w'`.
Go GC/memory/scheduler and Java option environment overrides were unset.

Each case has 21 fresh-process pairs, alternating Java/Go order, with one equal
warmup and a separate first pair excluded from the table. No trial was removed
or retried. Whole-process latency includes startup and output writing. p50 is
the median; p95 uses nearest rank. Page-cache state was uncontrolled. Output
comparisons ran outside timing, including OTLP timestamps/values and heatmap
method IDs/colors/timelines.

Values are **Java / Go**. RSS is MiB and latency is milliseconds. Speedup is
Java divided by Go; below 1 means Go is slower.

| Case | Median peak RSS | p95 peak RSS | p50 latency | p95 latency | p50 / p95 speedup |
|---|---:|---:|---:|---:|---:|
| Startup, 100k samples | 46.78 / 5.05 | 47.14 / 5.14 | 59.28 / 6.50 | 76.87 / 6.67 | 9.123× / 11.526× |
| Throughput, 10M samples | 198.48 / 5.19 | 199.33 / 5.44 | 220.51 / 269.43 | 282.20 / 315.35 | 0.818× / 0.895× |
| Diverse stacks, 2M, collapsed | 219.22 / 41.72 | 221.27 / 44.44 | 530.70 / 216.70 | 576.68 / 235.77 | 2.449× / 2.446× |
| Diverse stacks, 2M, heatmap | 309.75 / 55.73 | 324.17 / 59.39 | 721.52 / 727.79 | 828.27 / 821.60 | 0.991× / 1.008× |
| Deep shared constant pool | 174.77 / 54.56 | 176.38 / 55.23 | 295.50 / 408.77 | 415.30 / 432.66 | 0.723× / 0.960× |
| Limited batch, eight inputs | 208.45 / 7.28 | 210.34 / 7.55 | 365.79 / 489.34 | 460.56 / 684.93 | 0.748× / 0.672× |
| OTLP, 2M events | 181.09 / 14.44 | 183.23 / 15.06 | 181.66 / 893.06 | 236.73 / 1040.02 | 0.203× / 0.228× |
| Real Span API request filter | 42.78 / 6.02 | 42.97 / 6.09 | 69.39 / 4.66 | 72.15 / 5.08 | 14.906× / 14.215× |
| Span filter stress | 120.66 / 9.77 | 122.33 / 10.02 | 276.50 / 116.99 | 367.14 / 125.09 | 2.364× / 2.935× |

The diverse cases use `--lines`. The deep input has two chunks, each with
20,000 depth-128 stacks sharing method identities. The limited batch converts
eight 2M-event inputs with `--memory-limit 16M --max-rss 32M`. The Span cases
use `--cpu --tag request` and `--cpu --tag selected --latency 10us`; the stress
input contains 2M samples and 100,003 spans across four chunks.

The table uses each launched command's `wait4` RSS high-water mark, excluding
the harness. In limited mode this is not a simultaneous supervisor/worker sum.
Five separate RSS-only process-tree measurements gave median Java/Go peaks of
211.281/11.625 MiB. Sampled sums count shared pages per process and may miss
short-lived children; they are excluded from latency statistics.

Lower RSS does not imply lower latency. OTLP's bounded grouping and disk
spilling preserve per-event timestamps without keeping their arrays in RAM,
at the cost shown above. The 10M stream, deep stacks and limited batch also
remain slower than Java. These results describe this host and workload set.
See [TESTING.md](TESTING.md) for the benchmark helper and correctness checks.
