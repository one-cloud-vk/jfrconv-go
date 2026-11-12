# Testing

## Local checks

Go tests use the checked-in fixtures. Python 3.12+ runs the independent OTLP
decoder tests; Node.js runs UI comparisons. Java and async-profiler are needed
only for reference tests.

```sh
go test ./...
go vet ./...
python3 -m unittest discover -s tools -p 'test_*.py'
```

Set `JFR_REAL_INPUTS` to a colon-separated list of real JDK/async-profiler JFR
files to include them in the Go reader integration test. That optional test
skips when the variable is absent.

## Java reference and UI

Build the pristine converter at async-profiler tag `v4.5`, commit
`11aaea310cb75843f0e7532365d8cedc8d0e14c8`, with JDK 21:

```sh
git clone --depth 1 --branch v4.5 https://github.com/async-profiler/async-profiler.git work/async-profiler
export JAVA_HOME=/path/to/jdk21
export PATH="$JAVA_HOME/bin:$PATH"
make -C work/async-profiler -j2 all build-test-java
```

Use that converter JAR for differential checks. `--real` adds recordings to the
synthetic fixture cases and can be repeated.

```sh
python3 tools/validate.py --go bin/jfrconv \
  --java-jar work/async-profiler/build/jar/jfr-converter.jar \
  --real recording.jfr
```

To compare one flamegraph with the original renderer:

```sh
java -jar work/async-profiler/build/jar/jfr-converter.jar \
  --cpu tests/fixtures/mixed.jfr work/reference.html
bin/jfrconv --cpu tests/fixtures/mixed.jfr work/native.html
node tools/ui_renderer_parity.js --java work/reference.html --go work/native.html --strict
```

The renderer test uses a deterministic random seed and a mocked DOM/canvas. It
compares draw and event behavior, rather than browser font rasterization.
`tools/heatmap_colors.js` separately compares used method IDs and RGB values;
`tools/heatmap_decode.js` compares decoded samples and timeline data.

## Published converter and Span API tests

The pinned scope contains 27 declarations: 24 converter consumers, including
five direct OTLP cases, and three official Span producer/API tests. Eight
configurations are Linux-only. This is the converter/Span scope, not the whole
async-profiler test suite.

The runner needs JDK 21, make, a C/C++ compiler, and permission to profile its
own JVMs. It also needs the test-only protobuf runtime. Download it once; the
prepare command checks its SHA-256 before use:

```sh
curl -fL https://repo.maven.apache.org/maven2/com/google/protobuf/protobuf-java/4.31.1/protobuf-java-4.31.1.jar \
  -o work/protobuf-java-4.31.1.jar
python3 tools/upstream_tests.py prepare \
  --source work/async-profiler --dest work/upstream-native \
  --protobuf-jar work/protobuf-java-4.31.1.jar
python3 tools/upstream_tests.py run \
  --stage work/upstream-native --output work/public-tests \
  --native "$PWD/bin/jfrconv" \
  --reference-jar "$PWD/work/async-profiler/build/jar/jfr-converter.jar"
```

Use fresh staging/results directories. `prepare` archives the pinned Git commit,
so local changes in the upstream checkout are excluded. `run` builds the
profiler and tests, checks the 27-case scope and verifies assertion-source hashes
before and after execution. It writes logs and JSON results under `--output`.
Use `--no-build` only when the stage has already been built. To test the
unmodified Java baseline, prepare another stage with `--java-only`, then run it
without `--native` or `--reference-jar`.

Only transport entry points are redirected: converter calls invoke the native
CLI, and direct OTLP assertions inspect its output bytes. The original latency
visitor still runs in Java; the runner additionally requires native/Java
stack-count equality on its exact recording and latency threshold. The Java
assertions are unchanged. These adapters are test tools, not runtime
dependencies of the converter.

## Benchmarks

Run benchmarks serially, with other builds and profilers stopped:

```sh
python3 tools/benchmark.py --go bin/jfrconv \
  --java-jar work/async-profiler/build/jar/jfr-converter.jar \
  --input recording.jfr --repeat 21 --warmup 1
```

Each pair is checked for equivalent output outside the timers. Results retain
all raw trials, the first pair and warmups. Peak process RSS comes from `wait4`;
`--tree-rss-runs 5` adds separately instrumented process-tree samples. See
[BENCHMARKS.md](BENCHMARKS.md) for the recorded comparison and metric limits.
