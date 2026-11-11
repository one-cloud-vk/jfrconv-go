# Architecture quorum — 2026-11-11

Before implementation, three independent agents reviewed the design in the role of Go principal developers. These were agent reviews, not external human endorsements. All three voted CONDITIONAL APPROVE; implementation began only after all final votes arrived.

| Reviewer | Focus | Vote |
|---|---|---|
| go_principal_memory | Heap ownership, arenas, GC and RSS limits | Conditional approve |
| go_principal_compat | JFR/UI/heatmap reference ordering | Conditional approve |
| go_principal_perf | Native Go hot paths and measurement gates | Conditional approve |

Accepted conditions: native Go with no Rust/Java production subprocess and no cgo; 128 KiB streaming parser; pointer-free flat structures and integer IDs; chunk ownership; bounded external sorts/spools; no per-event objects, goroutines or channels; no whole-file mmap or large retained pools. Java UTF-16 ordering must be explicit.

Exact heatmap ordering must reproduce Java Dictionary history (including capacity across clear), Method identity (class, method, location, type, start), StackStorage encounter order and new-LZ-edge frequency before compaction. The LZ lookup needs bounded cache and disk fallback. Repeated samples can be skipped only after an entire stack pass creates no edges. Corrected data must not be corrupted to reproduce the legacy extra-stack aliasing bug; known legacy-bug cases need separate provenance.

Go runtime memory limits are soft and are not RSS limits. Do not use low RLIMIT_AS as an RSS proxy for Go. Require actual-RSS supervision, scratch cleanup and staged publication; strict whole-tree Linux limits use cgroup memory.max. Distinguish worker RSS, simultaneous process-tree RSS, live Go heap and runtime retained bytes.

Publication gates: unchanged published converter assertions; semantic/UI tests; method-ID/RGB parity with ties/unused stacks/locations/start flags/cross-chunk dictionaries; spill fallback; long-batch plateaus and limit-failure preservation; predeclared 21-pair fresh-process performance matrix, equal warmups and alternating order, output equivalence, all trials retained. RSS is the primary performance gate, latency p50/p95 secondary. Do not promise Go RSS below Rust before measuring.

Primary references: [Go GC guide](https://go.dev/doc/gc-guide), [SetMemoryLimit](https://pkg.go.dev/runtime/debug#SetMemoryLimit), [FreeOSMemory](https://pkg.go.dev/runtime/debug#FreeOSMemory), [allocator source](https://go.dev/src/runtime/malloc.go).
