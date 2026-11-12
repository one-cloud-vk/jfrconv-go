# Reproduce the Go0.2.0 / Java4.5 comparison

The immutable declaration-time plan is [plan.json](../plan.json). The original runner is retained as [original-run_matrix45.py](original-run_matrix45.py), with its actual workspace paths. It is an execution snapshot, not a portable command. [replay_matrix.py](replay_matrix.py) is a separate adapter written after measurement; it uses the byte-identical measured helper/decoder snapshots in `tools/` and preserves the nine declared cases, N=21, one equal warmup, separate first pair, alternating order, and every-pair output proofs. It creates new results and never rewrites the published arrays.

Measured native SHA256: `a9d7e8fc1fb440157df5f56ae34029e78cdf5b79005bc38d7a9406da4df1feb5`. Pristine4.5 converter JAR SHA256: `ded43c350f75766b82570ac297c9dd3c3efd69e73de26474615cf2df90e1fc94`. Source commit: `11aaea310cb75843f0e7532365d8cedc8d0e14c8`. Final source freeze is `680a235d0231be4c6c001aea6b633ece193b19893f7700f89128e7b627147897`; original build/Linux snapshot and the two-helper amendment are retained separately. Rebuilding a JAR can change its archive hash; a replay records actual artifact hashes and whether they match the measured artifacts.

Install Python3, Node, Go1.27.4, JDK21 and make, then build the native application as documented in the project README. Build the pristine async-profiler converter JAR at the exact commit with `JAVA_HOME=/path/to/jdk21 make -j2 jar`. The original reference build also built the native profiler and Java tests; its build and source manifests are retained above. Reproducing that full public-suite build additionally needs a C/C++ toolchain and the test protobuf dependency documented in the public reproduction instructions. JDK21.0.7, macOS15.8.2 arm64 and eight logical CPUs were used for the measurements. The native binary uses CGO_ENABLED=0, trimpath and stripped release flags. GOGC/GOMEMLIMIT/GOMAXPROCS/GODEBUG and Java option environment variables were unset.

From the project root, with fresh output directories:

```sh
python3 benchmarks/v0.2.0-java45/reproduction/generate_inputs.py --output /tmp/jfrconv45-inputs
python3 benchmarks/v0.2.0-java45/reproduction/replay_matrix.py \
  --native "$PWD/bin/jfrconv" --go-tool /path/to/go1.27.4/bin/go \
  --java /path/to/jdk21/bin/java \
  --java-jar /path/to/pristine-4.5/build/jar/jfr-converter.jar \
  --inputs /tmp/jfrconv45-inputs --output /tmp/jfrconv45-replay
```

Use `--dataset NAME` on the input adapter or `--case NAME` on the replay adapter for a subset. Every input is checked against the original plan's size/SHA256. The standard and deep fixture writer is the unchanged [fixtures.py](fixtures.py). The synthetic Span generator is preserved as [original-generate_span_stress.py](original-generate_span_stress.py); the input adapter implements the same events and checks the exact recorded SHA256. Synthetic datasets are deliberately omitted from the package because they regenerate deterministically.

The small [span-api45.jfr](span-api45.jfr) is the actual measured recording, produced through the official4.5 API by [OfficialSpanWorkload.java](OfficialSpanWorkload.java). It contains three real chunks and is shipped so the real-Span row uses the identical bytes. Its [manifest](span-api45-manifest.json) records the profiler/API hashes and spans. Running that producer again creates a comparable recording with different timestamps/samples, rather than the archived input SHA.

Run serially after other compilers/profilers finish. Page-cache state is uncontrolled. Latency is whole-process time including output writing; p50 is median and p95 is nearest rank. Every first/warmup/measured pair is checked outside the timers. Heatmaps include the complete decoded timeline and every used method ID/RGB. OTLP4.5 includes all ordered timestamps/selected values, group/chunk boundaries, sample/period types and sampling period. No trial was removed or retried.

The limited-batch row additionally runs five separately instrumented process-tree RSS pairs. The operating environment must allow read-only `ps` inspection of PID/parent/RSS for the replay's own children; these diagnostics are excluded from latency percentiles. Sampled sums double-count shared pages and can miss short-lived children. The main table uses `wait4` command high-water RSS. macOS bytes and Linux KiB are normalized by the helper.

[Raw reports](../raw/), [original matrix](../matrix-summary.json), [independent recomputation](../independent-audit.json), [public4.5 reproduction](../../../reports/releases/v0.2.0/public/reproduction/README.md). No measured executables, Java JARs, large inputs or generated output HTML/protobuf files are duplicated here.
