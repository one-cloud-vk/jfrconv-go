# Reproduce v0.1.0 evidence

The actual native executable hash is `144204d7e755411903b058f5153aa583bd4adf4c1212667c52d73ae75c85bca6`. The benchmark helper hash is `f9a89040e46c1140bf274c59c61e325100b1b9f7eae63368794117cfde664090`. This package retains all raw trial/proof JSON and the unchanged declaration-time execution plan, including the shared input-pool amendment. Linux used the earlier helper hash; the pool-only helper change is recorded explicitly and was tested on macOS.

The Python fixture writer below is copied byte-for-byte from the Go project's test generator. It is standalone test-data code and invokes neither Go nor Rust. SHA256: `bb3f7b7fdb28c2778901c45b5100ca1b5c7b9b75995e03a173f4f0e11826c6ef`.

From the project root:

```sh
python3 benchmarks/v0.1.0-macos/reproduction/fixtures.py /tmp/deep-shared.jfr --deep-shared --chunks 2
python3 benchmarks/v0.1.0-macos/reproduction/verify_final_matrix.py --matrix benchmarks/v0.1.0-macos/matrix-summary.json --output /tmp/matrix-audit.json
```

The deep input must be33,209,440 bytes, SHA256 `6e901634586c5e7ca3e2ef8d2e4ae181f77ff7a83b2c0492d8b4a9462d204a61`: two chunks, 20,000 raw stacks/chunk, 128 shared methods/frames per stack, line/BCI differences, 40,000 samples. Without `--lines`, all samples merge to one 128-frame path.

Synthetic dataset invocations are in `../execution-plan.json`. Generate event-count inputs with `--samples N --chunks 4 --stacks 3`, or `--samples 2000000 --chunks 4 --stacks 50000` for the diverse dataset. Real JDK/AP input hashes identify the locally generated recordings; timing depends on host/runtime. All cases used N=21, including dense heatmap (first Java run was below 60 seconds, so no exception was invoked).

Use the project's `tools/benchmark.py --help` with the matching native flag, original 4.1 converter JAR, JDK 21 and `--input-pool /tmp/jfrconv-input-pool`. The pool makes one read-only physical copy per SHA; case aliases link only to that copy, never to mutable originals. Public test evidence is under `reports/public-macos/v0.1.0/`.
