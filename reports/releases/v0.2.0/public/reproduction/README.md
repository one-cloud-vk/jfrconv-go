# Reproduce the pinned4.5 public scope

These versioned drivers execute the exact4.5 scope: 27 declarations, with 19 passing and eight OS skips on macOS, and all27 passing on Linux. This is the converter-consuming scope plus official Span API producers, not the entire async-profiler suite. [summary.json](../summary.json) retains native and pristine-Java reports, unchanged assertion hashes, transport snapshots and logs. The current project `tools/upstream_tests.py` is a legacy helper; it alone does not run this exact scope or the native OTLP/latency supplements.

Prerequisites: Python3, JDK21, make, a C/C++ toolchain and profiling permissions for your own JVMs. On macOS, the runner must have permission to create its own attach UNIX sockets; the final runs used the same unsandboxed profiling context as the baseline. Node is not required for this public suite. Public OTLP assertions need the test-only protobuf runtime4.31.1, which Make does not fetch. Its [provenance and hash](../reference/protobuf-runtime.json) are retained.

Use an exact checkout of async-profiler commit `11aaea310cb75843f0e7532365d8cedc8d0e14c8` (tag `v4.5`). Keep its tracked `test/gen/opentelemetry-gen-classes.jar`. Define absolute paths; use new directories for `stage`, `scope`, `native_out` and `java_out`:

```sh
project=/absolute/path/to/jfrconv-go
upstream=/absolute/path/to/pristine-async-profiler-4.5
stage=/tmp/jfrconv45-public-native
scope=/tmp/jfrconv45-scope
native_out=/tmp/jfrconv45-native-results
java_out=/tmp/jfrconv45-java-results
repro="$project/reports/releases/v0.2.0/public/reproduction"
manifest="$project/reports/releases/v0.2.0/public/upstream-source.json"
export JAVA_HOME=/absolute/path/to/jdk21
export PATH="$JAVA_HOME/bin:$PATH"

mkdir -p "$upstream/test/deps"
curl -fL https://repo.maven.apache.org/maven2/com/google/protobuf/protobuf-java/4.31.1/protobuf-java-4.31.1.jar \
  -o "$upstream/test/deps/protobuf-java-4.31.1.jar"
python3 -c 'import hashlib,sys; assert hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest()=="d60dfe7c68a0d38a248cca96924f289dc7e1966a887ee7cae397701af08575ae"' \
  "$upstream/test/deps/protobuf-java-4.31.1.jar"
make -C "$upstream" -j2 all build-test-java
python3 "$repro/stage_public45.py" \
  --source "$upstream" --dest "$stage" \
  --transport-helpers "$repro/transport_helpers.py" \
  --bridge-template "$repro/GoBridge.java" --upstream-manifest "$manifest"
make -C "$stage" -j2 all build-test-java
mkdir "$scope"
(cd "$stage" && "$JAVA_HOME/bin/javac" \
  -cp 'build/test.jar:build/jar/*:build/lib/*:test/deps/*:test/gen/*' \
  -d "$scope" "$repro/PublicScope.java")
python3 "$repro/run_public45.py" --stage "$stage" --output "$native_out" \
  --native "$project/bin/jfrconv" --java "$JAVA_HOME/bin/java" \
  --reference-jar "$upstream/build/jar/jfr-converter.jar" \
  --source-manifest "$manifest" --scope-class-dir "$scope"
python3 "$repro/run_public45.py" --stage "$upstream" --output "$java_out" \
  --java "$JAVA_HOME/bin/java" \
  --reference-jar "$upstream/build/jar/jfr-converter.jar" \
  --source-manifest "$manifest" --scope-class-dir "$scope"
```

The staging driver checks the pinned source manifest before copying and verifies every public assertion source is unchanged. The test-only transport changes static converter entry points and `Output.convertJfrToCollapsed` to invoke the real native CLI with the actual requested paths, including `/dev/null`. A weak map registers filename-backed Java readers; `JfrToOtlp.dump` writes actual native OTLP bytes, inspected by the original unchanged FromJfr assertions. Original Java `convert()` can execute before this dump; this bridge does not claim that intermediate Java collection was native.

The original `latencyFilter` anonymous visitor remains a Java assertion. On a successful native suite, the driver additionally requires complete native/Java collapsed-map equality on its exact recording and490ms wall filter, with showcase1/2 present and showcase3 absent. The final [report](../native-macos/latency-native-oracle/report.json) and small [actual input](../native-macos/latency-native-oracle/input.jfr) are included. To replay only this supplement:

```sh
python3 "$repro/latency_native_oracle.py" --java "$JAVA_HOME/bin/java" \
  --jar "$upstream/build/jar/jfr-converter.jar" \
  --recording "$repro/../native-macos/latency-native-oracle/input.jfr" \
  --native "$project/bin/jfrconv" --minimum-ms 490 --output /tmp/jfrconv45-latency
```

The native test harness uses serial tests and retryCount=0. New profiling recordings vary with scheduling; raw results describe the original final run. The staged driver requires filename-backed readers in its OTLP bridge; the pinned FromJfr cases use those readers. This transport exists only for tests and is not part of the native application's runtime.
