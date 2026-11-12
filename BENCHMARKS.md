# jfrconv-go performance and validation evidence

## v0.2.0: pinned Java 4.5 and official Span API

Native Go 0.2.0 uses **68.8–97.4% less median RSS across all nine declared cases** than pristine Java jfrconv 4.5; p95 RSS is lower in all nine as well. Latency wins are **4/9 at p50 and 5/9 at p95**. Official-API request filtering is about 15× faster at p50. The new per-event OTLP case is about 4.9× slower, with 92.0% lower median RSS. All slower cases and their complete raw trials remain below.

This is a new comparison against tag `v4.5`, commit `11aaea310cb75843f0e7532365d8cedc8d0e14c8`. The historical Java 4.1 and native 0.1.x results below retain their original versions and artifacts. Native SHA256: `a9d7e8fc1fb440157df5f56ae34029e78cdf5b79005bc38d7a9406da4df1feb5`. Pristine 4.5 converter JAR SHA256: `ded43c350f75766b82570ac297c9dd3c3efd69e73de26474615cf2df90e1fc94`. Final source freeze SHA256: `680a235d0231be4c6c001aea6b633ece193b19893f7700f89128e7b627147897`; the original build/Linux snapshot `73968c9525ecf8895d3344f29fed525aa4445d1a23487bc6ff6920c0ff88f5d8` and explicit two-helper amendment are preserved.

Host: macOS 15.8.2 arm64, eight logical CPUs. Java: OpenJDK 21.0.7 Homebrew, executable `/opt/homebrew/opt/openjdk@21/bin/java`. Go: 1.27.4, CGO_ENABLED=0, trimpath and stripped release binary. Go GC/memory/scheduler overrides and Java option environment variables were unset. Every row has **21 measured pairs**, one equal warmup per implementation and a separately retained first fresh-process pair. Order alternates within each pair. No trial was removed or retried. Whole-process latency includes startup and output writing; p95 is nearest rank. File page-cache state is uncontrolled. All compiler, correctness, profiler and container jobs ended before this serial matrix ran.

Each first, warmup, measured and diagnostic pair passes output comparison outside the timed intervals. Heatmap checks include full timeline/stack/count content and every used method ID/RGB. OTLP checks resolve every ordered timestamp/selected-value pair, explicit versus implicit values, global stacks/attributes, profile/group/chunk boundaries, sample/period types and sampling period. Inputs and helper/decoder snapshots are pinned by SHA256 in the declaration-time plan. Raw reports checkpoint every process and verification.

Values below are **Java / Go**. RSS is MiB, latency is milliseconds, and speedup is Java divided by Go; a ratio below 1 means Go is slower. The diverse rows use `--lines`. The limited batch processes eight 2M-event inputs with native `--memory-limit 16M --max-rss 32M`. The two Span rows use `--cpu --tag request` and `--cpu --tag selected --latency 10us`, respectively.

| Case | Median RSS | p95 RSS | p50 latency | p95 latency | p50 / p95 speedup |
|---|---:|---:|---:|---:|---:|
| [startup-100k](benchmarks/v0.2.0-java45/raw/startup-100k.json) | 46.78 / 5.05 | 47.14 / 5.14 | 59.28 / 6.50 | 76.87 / 6.67 | 9.123× / 11.526× |
| [throughput-10m](benchmarks/v0.2.0-java45/raw/throughput-10m.json) | 198.48 / 5.19 | 199.33 / 5.44 | 220.51 / 269.43 | 282.20 / 315.35 | 0.818× / 0.895× |
| [diverse-2m-collapsed](benchmarks/v0.2.0-java45/raw/diverse-2m-collapsed.json) | 219.22 / 41.72 | 221.27 / 44.44 | 530.70 / 216.70 | 576.68 / 235.77 | 2.449× / 2.446× |
| [diverse-2m-heatmap](benchmarks/v0.2.0-java45/raw/diverse-2m-heatmap.json) | 309.75 / 55.73 | 324.17 / 59.39 | 721.52 / 727.79 | 828.27 / 821.60 | 0.991× / 1.008× |
| [deep-shared-cp](benchmarks/v0.2.0-java45/raw/deep-shared-cp.json) | 174.77 / 54.56 | 176.38 / 55.23 | 295.50 / 408.77 | 415.30 / 432.66 | 0.723× / 0.960× |
| [batch-limited-eight-inputs](benchmarks/v0.2.0-java45/raw/batch-limited-eight-inputs.json) | 208.45 / 7.28 | 210.34 / 7.55 | 365.79 / 489.34 | 460.56 / 684.93 | 0.748× / 0.672× |
| [otlp-2m-events](benchmarks/v0.2.0-java45/raw/otlp-2m-events.json) | 181.09 / 14.44 | 183.23 / 15.06 | 181.66 / 893.06 | 236.73 / 1040.02 | 0.203× / 0.228× |
| [span-api-real-request](benchmarks/v0.2.0-java45/raw/span-api-real-request.json) | 42.78 / 6.02 | 42.97 / 6.09 | 69.39 / 4.66 | 72.15 / 5.08 | 14.906× / 14.215× |
| [span-filter-stress](benchmarks/v0.2.0-java45/raw/span-filter-stress.json) | 120.66 / 9.77 | 122.33 / 10.02 | 276.50 / 116.99 | 367.14 / 125.09 | 2.364× / 2.935× |

The 10M stream takes 269.43 versus Java 220.51 ms at p50 and 315.35 versus 282.20 ms at p95. Diverse heatmap p50 is slightly slower, 727.79 versus 721.52 ms; p95 is slightly faster. Deep constant-pool conversion takes 408.77 versus 295.50 ms at p50 and 432.66 versus 415.30 ms at p95. Limited-batch latency is 489.34 versus 365.79 ms at p50 and 684.93 versus 460.56 ms at p95. These are retained losses, not excluded outliers.

The new 4.5 OTLP format preserves per-event timestamp/value arrays. Bounded grouping and spill processing trade latency for memory: **14.44 versus 181.09 MiB median RSS**, but **893.06 versus 181.66 ms p50** and **1040.02 versus 236.73 ms p95**. Earlier aggregate-OTLP 4.1 timings cannot stand in for this format. The real official-API recording filters request spans in **4.66 versus 69.39 ms**, using **6.02 versus 42.78 MiB**. The separate synthetic stress contains 2M execution samples and 100,003 spans across four chunks; all pairs select the identical 250,135 samples and eight paths.

Median RSS reductions range from 68.78% in the deep constant-pool case to 97.39% in the 10M stream; p95 reductions range from 68.68% to 97.27%. The deep input has two chunks, 20,000 raw depth-128 stacks per chunk and shared method identities; without `--lines`, its 40,000 samples merge to one path. Go uses 54.56 MiB versus Java 174.77 MiB median RSS there. RSS depends on retained dictionaries and distinct paths, not only event count.

The main table records each launched command's `wait4` high-water RSS, excluding the Python harness. Five extra RSS-only limited-batch pairs sampled simultaneous process trees: Java / Go median peaks **211.281 / 11.625 MiB**, largest observed peaks **211.734 / 11.969 MiB**. Every monitor completed and observed both native supervisor and worker. Shared pages are counted per process and short-lived children may be missed; these sampled sums are lower bounds and are excluded from latency statistics. Go's runtime memory target is soft; `--max-rss` polls its worker and permits brief overshoot, with supervisor RSS outside the worker limit. The separate Linux 64 MiB cgroup test processed 64 inputs with kernel `memory.peak` **16.19 MiB**, no OOM and no swap. Kernel cgroup memory also includes charged page cache and differs from process RSS.

The [independent raw-stat audit](reports/releases/v0.2.0/performance-audit.json) recomputes all medians, p95 values, maxima and ratios and verifies output proofs/order. [Nine-row summary](benchmarks/v0.2.0-java45/summary.json), [complete original matrix](benchmarks/v0.2.0-java45/matrix-summary.json), [predeclared plan](benchmarks/v0.2.0-java45/plan.json), [portable reproduction](benchmarks/v0.2.0-java45/reproduction/README.md). The small actual official-API recording is included; large deterministic inputs, measured executables/JARs and bulky outputs are omitted.

Final functional gates include 86 macOS native tests, 81 semantic checks, exact original 4.5 UI scripts through 357 phases/882 assertions at each DPR 1 and 2, 22 exact heatmap comparisons and 60 fresh official-Span/Unicode/trace differential pairs. Public scope is 27 pinned-tag declarations: [macOS native and pristine Java](reports/releases/v0.2.0/public/summary.json) each pass 19 with eight OS skips; [Linux](reports/releases/v0.2.0/linux/summary.json) passes all 27. Original public assertion sources remain unchanged. The direct-library OTLP tests inspect native dump bytes; the Java-only anonymous latency visitor is explicitly supplemented by required native/Java output equality on its exact recording. [Public reproduction drivers](reports/releases/v0.2.0/public/reproduction/README.md) and [complete release evidence](reports/releases/v0.2.0/evidence.json) record this scope and the test-only transport.

## v0.1.1: lower RSS and native old/new comparison

The compact stack representation and reused conversion scratch reduce deep constant-pool median RSS from **128.09 to 53.73 MiB (58.05%)**, and diverse collapsed RSS from **49.81 to 40.73 MiB (18.22%)**. Dense heatmap RSS falls 2.39%; the small/startup working set stays at the same runtime floor. Latency is mixed: deep and diverse cases improve, while 10M and limited-batch tail latency regress. Every result remains visible below.

This followup compares native **0.1.0 against 0.1.1**, with **21 measured pairs per case**, equal warmups, separate first pairs and alternating order. Canonical stack/count output and exact heatmap timeline/used-ID/RGB proofs pass after every pair outside timing. Ratios are old/new; below 1 means 0.1.1 is slower. No trial was removed or retried. The original 29-case Java matrix remains in the 0.1.0 section with its original binary/runtime hashes.

Old SHA256: `144204d7e755411903b058f5153aa583bd4adf4c1212667c52d73ae75c85bca6`. New SHA256: `785dfc5fd23cf07dae4e26f905d2699ee8c128a63e3256b7b1a255428170d6de`. Source manifest SHA256: `7ac225a55b826584e03b6301b39fbc8be66a094d7fc7be72e581aaccdab98930`. Go 1.27.4, arm64 macOS 15.8.2; GOGC/GOMEMLIMIT/GOMAXPROCS/GODEBUG remain unset.

| Case | Median RSS MiB, old / new | p95 RSS MiB, old / new | p50 ms, old / new | p95 ms, old / new | p50 / p95 speedup |
|---|---:|---:|---:|---:|---:|
| startup-100k | 5.17 / 5.17 | 5.27 / 5.23 | 6.08 / 6.03 | 6.86 / 7.12 | 1.008× / 0.963× |
| throughput-10m | 5.28 / 5.28 | 5.58 / 5.45 | 257.58 / 261.38 | 286.49 / 355.33 | 0.985× / 0.806× |
| diverse-2m-collapsed | 49.81 / 40.73 | 51.77 / 43.69 | 196.62 / 193.18 | 289.11 / 223.65 | 1.018× / 1.293× |
| diverse-2m-heatmap | 54.92 / 53.61 | 59.11 / 56.89 | 1159.83 / 1156.71 | 1663.62 / 1575.42 | 1.003× / 1.056× |
| deep-shared-cp | 128.09 / 53.73 | 132.41 / 55.17 | 422.82 / 392.50 | 450.42 / 441.99 | 1.077× / 1.019× |
| batch-limited-eight-inputs | 7.41 / 7.36 | 7.70 / 7.78 | 428.51 / 441.48 | 484.48 / 611.01 | 0.971× / 0.793× |

Median RSS is lower in 4/6 cases and equal in 2/6; p95 RSS is lower in 5/6. The limited-batch p95 RSS increase is 7.70→7.78 MiB (+1.01%). Latency improves in 4/6 p50 and 3/6 p95 cells. In particular, 10M p95 is 286.49→355.33 ms and limited-batch p95 is 484.48→611.01 ms. These measurements do not support a universal latency improvement.

Five additional limited-batch RSS-only pairs observed both supervisor and worker, without monitor errors. Old/new sampled tree medians are 11.875/11.922 MiB and largest observed peaks 12.313/12.188 MiB: approximately the same working-set floor. The sampled sums double-count shared pages and can miss short-lived children; they are lower bounds and are excluded from timing percentiles.

A separate, predeclared lower-limit diagnostic used identical `--memory-limit 48M --max-rss 96M` flags on the 33 MB deep input. Version 0.1.0 exits 75 and preserves the existing output. Version 0.1.1 succeeds and matches the unlimited baseline's complete 40,000-unit, 128-frame path. The normal stored-frame layout uses 13 bytes instead of 24, while optional high-ID slabs preserve arbitrary 64-bit method IDs; raw 32-bit line/BCI values are preserved. The admission/runtime target and polled worker RSS guard retain their documented limits; this test establishes the observed recording-specific result.

[Six-case summary](benchmarks/v0.1.1-native-comparison/summary.json), [all raw pairs/proofs](benchmarks/v0.1.1-native-comparison/comparison.json), [predeclared plan](benchmarks/v0.1.1-native-comparison/plan.json), [raw-stat recomputation audit](benchmarks/v0.1.1-native-comparison/recomputed-audit.json), [lower-limit diagnostic](benchmarks/v0.1.1-native-comparison/limit-diagnostic/report.json), [source manifest](benchmarks/v0.1.1-native-comparison/source-freeze.json), [reproduction](benchmarks/v0.1.1-native-comparison/reproduction/README.md). Functional/public/UI evidence is pinned in [0.1.1 release evidence](reports/releases/v0.1.1/evidence.json).

## v0.1.0: 29 predeclared Java 4.1 comparisons

The frozen native Go executable wins **median and p95 RSS in all 29 cases**. Median RSS is **26.1–97.5% lower** than Java; p95 RSS is 24.8–97.4% lower. Go wins latency in 25/29 p50 cells and 26/29 p95 cells. The 10M stream, both long batches and deep constant-pool case have slower medians; they remain in the full table and raw trials.

Native SHA256: `144204d7e755411903b058f5153aa583bd4adf4c1212667c52d73ae75c85bca6`. Benchmark helper SHA256: `f9a89040e46c1140bf274c59c61e325100b1b9f7eae63368794117cfde664090`. Original async-profiler 4.1 converter JAR SHA256: `2464b34abd47243941d81d53110591d66d7db3b3fae46c79fc85057eeddda19b`.

Host: macOS 15.8.2 arm64, 8 logical CPUs. Java baseline: **OpenJDK 21.0.7 Homebrew**, executable `/opt/homebrew/opt/openjdk@21/bin/java`, source revision `5930966a92860f6e5d2d89ab6faab5815720bad9`. Go 1.27.4, CGO_ENABLED=0, trimpath and stripped release binary; GOGC/GOMEMLIMIT/GOMAXPROCS/GODEBUG were unset.

Every case has **21 measured pairs**, one equal warmup per implementation and a separately retained first fresh-process pair. Order alternates within every measured pair. Each command includes startup and output writing; p50 is the median and p95 is the nearest-rank percentile. No outlier was removed. The dense heatmap's first Java process was below 60 seconds, so the smaller-N exception was unused. File page-cache state is uncontrolled. Go and Rust run in separate Java-paired batches.

Every first, warmup, measured and diagnostic pair passes canonical output comparison outside the timed intervals. pprof checks value vectors, resolved names/locations/labels, sample types/units and profile times; producer comments and synthetic producer mapping titles differ intentionally. OTLP checks values, locations/attributes and chunk/timestamp structure. Heatmap checks timeline/stack/count content and **every used method ID and RGB**, with full sorted-row digests. Inputs use a shared read-only SHA256 pool; aliases link only to the trusted pool. Existing output is removed before each command, and raw JSON is checkpointed after every process and verification.

Summary ranges across all 29 cases: p50 speedup **0.688–14.218×**, p95 speedup **0.774–14.179×**. A ratio below 1 means the native application is slower.

### Complete frozen matrix

Each row uses N=21. RSS columns are MiB; latency columns are milliseconds. Values are **Java / Go**. `--lines` is enabled on every diverse 2M row; real recordings use all execution states.

| Case | Median RSS | p95 RSS | p50 latency | p95 latency | p50 / p95 speedup |
|---|---:|---:|---:|---:|---:|
| [startup-100k](benchmarks/v0.1.0-macos/raw/startup-100k.json) | 46.86 / 5.22 | 47.31 / 5.36 | 62.56 / 6.87 | 77.16 / 7.86 | 9.108× / 9.811× |
| [low-2m-collapsed](benchmarks/v0.1.0-macos/raw/low-2m-collapsed.json) | 102.53 / 5.27 | 103.69 / 5.55 | 120.37 / 56.07 | 129.43 / 63.82 | 2.147× / 2.028× |
| [low-2m-html](benchmarks/v0.1.0-macos/raw/low-2m-html.json) | 103.22 / 5.27 | 103.91 / 5.47 | 121.51 / 55.50 | 139.87 / 60.17 | 2.189× / 2.325× |
| [low-2m-pprof](benchmarks/v0.1.0-macos/raw/low-2m-pprof.json) | 102.28 / 5.23 | 103.14 / 5.39 | 116.18 / 55.28 | 156.77 / 66.28 | 2.102× / 2.365× |
| [low-2m-pb.gz](benchmarks/v0.1.0-macos/raw/low-2m-pb.gz.json) | 102.70 / 5.59 | 103.66 / 5.69 | 118.98 / 57.61 | 136.05 / 61.98 | 2.065× / 2.195× |
| [low-2m-otlp](benchmarks/v0.1.0-macos/raw/low-2m-otlp.json) | 102.25 / 5.36 | 104.02 / 5.64 | 126.50 / 56.26 | 147.66 / 58.39 | 2.248× / 2.529× |
| [low-2m-heatmap](benchmarks/v0.1.0-macos/raw/low-2m-heatmap.json) | 255.69 / 6.38 | 256.91 / 6.67 | 297.80 / 130.30 | 384.92 / 136.44 | 2.285× / 2.821× |
| [throughput-10m](benchmarks/v0.1.0-macos/raw/throughput-10m.json) | 197.61 / 5.33 | 198.34 / 5.48 | 224.91 / 253.76 | 276.38 / 270.34 | 0.886× / 1.022× |
| [diverse-2m-collapsed](benchmarks/v0.1.0-macos/raw/diverse-2m-collapsed.json) | 220.44 / 46.80 | 226.25 / 50.44 | 541.41 / 188.67 | 586.44 / 212.80 | 2.870× / 2.756× |
| [diverse-2m-html](benchmarks/v0.1.0-macos/raw/diverse-2m-html.json) | 215.67 / 49.67 | 217.77 / 50.78 | 874.53 / 299.22 | 1144.35 / 451.28 | 2.923× / 2.536× |
| [diverse-2m-pprof](benchmarks/v0.1.0-macos/raw/diverse-2m-pprof.json) | 171.27 / 44.95 | 172.45 / 47.31 | 299.48 / 186.00 | 506.62 / 271.62 | 1.610× / 1.865× |
| [diverse-2m-pb.gz](benchmarks/v0.1.0-macos/raw/diverse-2m-pb.gz.json) | 168.28 / 45.53 | 170.28 / 47.41 | 365.98 / 170.28 | 430.46 / 193.06 | 2.149× / 2.230× |
| [diverse-2m-otlp](benchmarks/v0.1.0-macos/raw/diverse-2m-otlp.json) | 240.12 / 43.62 | 241.12 / 45.94 | 347.13 / 238.93 | 421.83 / 284.25 | 1.453× / 1.484× |
| [diverse-2m-heatmap](benchmarks/v0.1.0-macos/raw/diverse-2m-heatmap.json) | 292.14 / 53.33 | 294.05 / 59.09 | 5798.53 / 707.25 | 6033.83 / 794.71 | 8.199× / 7.593× |
| [real-jdk-collapsed](benchmarks/v0.1.0-macos/raw/real-jdk-collapsed.json) | 43.31 / 5.73 | 43.59 / 5.78 | 64.60 / 4.69 | 66.06 / 4.87 | 13.763× / 13.570× |
| [real-jdk-html](benchmarks/v0.1.0-macos/raw/real-jdk-html.json) | 44.09 / 5.73 | 44.39 / 5.86 | 69.26 / 4.87 | 72.10 / 5.08 | 14.218× / 14.179× |
| [real-jdk-pprof](benchmarks/v0.1.0-macos/raw/real-jdk-pprof.json) | 43.09 / 5.67 | 43.23 / 5.73 | 62.43 / 4.68 | 63.66 / 4.96 | 13.330× / 12.831× |
| [real-jdk-pb.gz](benchmarks/v0.1.0-macos/raw/real-jdk-pb.gz.json) | 43.73 / 6.08 | 44.08 / 6.14 | 65.61 / 4.76 | 67.49 / 4.92 | 13.777× / 13.720× |
| [real-jdk-otlp](benchmarks/v0.1.0-macos/raw/real-jdk-otlp.json) | 43.64 / 5.86 | 43.84 / 5.92 | 63.81 / 5.07 | 65.33 / 5.51 | 12.596× / 11.848× |
| [real-jdk-heatmap](benchmarks/v0.1.0-macos/raw/real-jdk-heatmap.json) | 173.47 / 7.08 | 173.89 / 7.12 | 90.14 / 6.84 | 93.24 / 7.71 | 13.174× / 12.090× |
| [real-ap41-collapsed](benchmarks/v0.1.0-macos/raw/real-ap41-collapsed.json) | 44.48 / 6.11 | 44.98 / 6.20 | 73.53 / 5.62 | 81.36 / 6.28 | 13.072× / 12.955× |
| [real-ap41-html](benchmarks/v0.1.0-macos/raw/real-ap41-html.json) | 45.45 / 6.06 | 45.97 / 6.12 | 72.93 / 5.91 | 87.11 / 6.96 | 12.347× / 12.514× |
| [real-ap41-pprof](benchmarks/v0.1.0-macos/raw/real-ap41-pprof.json) | 43.92 / 5.98 | 44.41 / 6.08 | 70.52 / 5.41 | 89.68 / 6.82 | 13.025× / 13.148× |
| [real-ap41-pb.gz](benchmarks/v0.1.0-macos/raw/real-ap41-pb.gz.json) | 44.12 / 6.48 | 44.34 / 6.56 | 67.18 / 5.58 | 68.46 / 5.79 | 12.046× / 11.827× |
| [real-ap41-otlp](benchmarks/v0.1.0-macos/raw/real-ap41-otlp.json) | 44.22 / 6.17 | 44.45 / 6.41 | 66.22 / 5.72 | 68.25 / 6.01 | 11.580× / 11.365× |
| [real-ap41-heatmap](benchmarks/v0.1.0-macos/raw/real-ap41-heatmap.json) | 174.58 / 8.34 | 175.25 / 8.44 | 100.13 / 8.31 | 112.39 / 10.67 | 12.056× / 10.531× |
| [batch-eight-inputs](benchmarks/v0.1.0-macos/raw/batch-eight-inputs.json) | 207.58 / 9.50 | 210.08 / 9.81 | 348.70 / 431.17 | 366.73 / 443.92 | 0.809× / 0.826× |
| [batch-limited-eight-inputs](benchmarks/v0.1.0-macos/raw/batch-limited-eight-inputs.json) | 207.97 / 7.42 | 211.00 / 7.75 | 348.87 / 438.92 | 394.55 / 455.29 | 0.795× / 0.867× |
| [deep-shared-cp](benchmarks/v0.1.0-macos/raw/deep-shared-cp.json) | 174.55 / 129.06 | 176.02 / 132.33 | 303.85 / 441.59 | 367.59 / 474.85 | 0.688× / 0.774× |

The startup case uses 5.22 MiB versus Java 46.86 MiB median RSS. The 2M low-cardinality stream uses 5.27 MiB versus 102.53 MiB and takes 56.07 ms versus 120.37 ms. The 2M diverse collapsed case uses 46.80 MiB versus 220.44 MiB and takes 188.67 ms versus 541.41 ms. At 10M, RSS stays5.33 MiB versus 197.61 MiB, but median latency is 253.76 ms versus 224.91 ms. The deep stress case uses 129.06 MiB versus 174.55 MiB, with 441.59 ms versus 303.85 ms median latency.

### RSS limits and sustained work

The two batch rows process eight copies of the 2M input in one CLI invocation. The limited row uses `--memory-limit 16M --max-rss 32M`. The main table records each launched command's `wait4` high-water RSS, excluding the Python harness; it is not a simultaneous supervisor-plus-worker sum.

Five separate, instrumented RSS-only pairs sampled process trees using only PID/parent/RSS fields. All monitors completed without errors and observed both native processes. Java / Go median sampled tree peaks were **208.11 / 11.72 MiB**; largest observed peaks were **209.73 / 12.17 MiB**. These sums count shared pages per process and can miss short-lived children; they are lower bounds and are excluded from latency statistics.

Go --memory-limit combines conservative application admission with the Go runtime soft memory target. GOMEMLIMIT/SetMemoryLimit is not an RSS cap. The separate `--max-rss` supervisor monitors the worker; its polling interval permits brief overshoot and its own RSS is outside the worker limit. Linux cgroup tests and cleanup/error behavior are retained in [Linux evidence](reports/linux/summary.json).

### Validation and retained provenance

Final [public macOS evidence](reports/public-macos/v0.1.0/summary.json) records 8 historical and 6 current published converter cases per native executable, with unchanged assertion-source hashes and actual requested output paths, including `/dev/null`. The historical Linux-only case is recorded separately in Linux evidence. Both earlier Go/JDK25 `regularPeak` workload-timing failures and exact same-recording Java replay are preserved under `reports/public-macos/v0.1.0/prior-jdk25-go/`. Full native public suites then passed with the same JDK21 executable as the original public Java baseline.

Final native candidates also pass 71 semantic checks. The UI proof covers 24 pairs, with 21 flamegraph HTML/CSS shells identical after removing generated script data. The complete original JavaScript produces 273 identical draw/event phases and 714 checks at each of DPR 1 and 2. Ten heatmap modes match timeline/count/stack content and used IDs/RGB (eight populated, two expected empty modes).

[All29-row summary](benchmarks/v0.1.0-macos/summary.json), [declaration-time execution plan](benchmarks/v0.1.0-macos/execution-plan.json), [independent raw-trial audit](benchmarks/v0.1.0-macos/independent-audit.json), [pool-helper provenance amendment](benchmarks/v0.1.0-macos/input-pool-amendment.json), [standalone reproduction instructions](benchmarks/v0.1.0-macos/reproduction/README.md). The Python deep-fixture generator is bundled for both projects; the33MB fixture, duplicated input copies, executables and bulky generated output files are omitted. All raw timing arrays, first/warmup records, errors/logs and per-pair semantic/RGB proofs are retained.

