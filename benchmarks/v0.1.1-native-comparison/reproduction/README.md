# Reproduce the native Go memory followup

Baseline 0.1.0 SHA256 `144204d7e755411903b058f5153aa583bd4adf4c1212667c52d73ae75c85bca6`; candidate 0.1.1 SHA256 `785dfc5fd23cf07dae4e26f905d2699ee8c128a63e3256b7b1a255428170d6de`. The source manifest and exact measured runner are bundled unchanged. The runner imports native-agnostic measurement/input-pool helpers; it directly executes old and new Go binaries.

From the project root, with both matching binaries available:

```sh
python3 benchmarks/v0.1.1-native-comparison/reproduction/verify_comparison.py --input benchmarks/v0.1.1-native-comparison/comparison.json --output /tmp/go-memory-audit.json
python3 benchmarks/v0.1.1-native-comparison/reproduction/compare_native.py --helpers tools/benchmark.py --plan /path/to/local-plan.json --baseline /path/to/0.1.0 --candidate /path/to/0.1.1 --candidate-sha256 785dfc5fd23cf07dae4e26f905d2699ee8c128a63e3256b7b1a255428170d6de --workdir /tmp/go-memory-comparison
```

The captured plan contains original absolute fixture paths; make a local copy and update paths/input-pool location before reproduction. Generate synthetic fixtures with `tools/fixtures.py`; deep uses `--deep-shared --chunks 2`, diverse uses `--samples 2000000 --chunks 4 --stacks 50000`. Dataset hashes/specifications are retained in plan.json. Keep runtime policy fixed as declared.

The separate 48 MiB admission /96 MiB worker-RSS diagnostic runs after all six timed cases and contributes no latency statistics. Its declaration, both actual outcomes and output-preservation proof are retained. The existing Java 29-case results remain version 0.1.0 evidence.
