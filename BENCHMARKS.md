# jfrconv-go performance and validation evidence

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

