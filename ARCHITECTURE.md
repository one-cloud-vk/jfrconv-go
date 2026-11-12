# Architecture and memory ownership

This is a native Go implementation, built with `CGO_ENABLED=0` and no external
modules. Production conversion does not launch Java, Rust or Node.js. Go 1.23
is the module minimum; the verified development compiler is recorded in
[reports/toolchain.json](reports/toolchain.json).

The design passed the three agent reviews in [QUORUM.md](QUORUM.md) before
implementation. The primary constraints are bounded working memory, explicit
chunk/profile ownership and preserved async-profiler **4.5** behavior. The
exact pinned commit and build/final source manifests are recorded in
[COMPATIBILITY.md](COMPATIBILITY.md).

## Streaming input and compact model

`internal/jfr` reads through a 128 KiB buffer. It does not load or map the
whole recording. Constant-pool tables belong to the current chunk and are
replaced at chunk boundaries. Six period settings survive chunks in a bounded
reader-owned table; replacing a value releases its previous budget charge.
Valid UTF-8 takes the direct arena path. Malformed UTF-8 is sized first, then
decoded directly into budgeted arena storage using Java replacement grouping;
there is no full temporary normalized string. Events are reused callback
values: consumers must resolve their IDs while the owning chunk is alive,
rather than retaining
event pointers. Stack traces are immutable views over dense 1024-frame slabs.
Separate method, location and kind columns use 13 bytes per ordinary frame,
plus slab headers and allocator rounding. Slabs containing wide method IDs add
a four-byte high column. Full 64-bit IDs and signed 32-bit line/BCI values are
preserved; viewing a frame does not allocate. Optional `TrackStackOrder`
reconstructs Java dictionary traversal only for the heatmap reference pass.

`internal/convert` interns immutable strings in an arena. Frames and prefix
nodes live in numeric slabs addressed by integer IDs. Their elements contain
no Go pointers, reducing the graph scanned by GC; growing a slab list does not
copy all previously stored nodes. Flat hash indexes resolve interned names,
frames and stack edges. A chunk-local path resolver reuses its description,
frame-ID and classification buffers across representative stacks; its string
references are cleared after each call. Per-chunk caches avoid a new object
for each CPU sample. There are no per-event goroutines or channels.

Each conversion owns its profile, input reader and spill files. It releases
them before another input is processed. Official Span filtering scans merged
per-thread intervals in a second event pass. Tags compare literally, latency
compares raw ticks, and interval bounds use the reference clock conversion
and exclusive end. Native leak analysis externally sorts allocations/frees
and resolves retained paths without keeping every event in
RAM. Outstanding allocations and selected span intervals remain
workload-dependent state; they are not a constant-size cache.

## Exact heatmap reference ordering

The original UI hashes numeric method IDs for its shades. The backend therefore
models Java's method and stack encounter order, including the 4.5 dictionary
key remapping before probing. A reference-order constant-pool dictionary tracks
occupied slots, resize history and logical capacity across chunk clears. A
second constant-pool-only pass ranks selected typed methods and stacks;
compact on-disk records preserve extra-event encounter order.

Identity includes class, method, packed line/BCI, frame kind and start role,
with typed thread/class/method keys. Thread frames own the start role when
requested. Weighted wall events use recorded wall/interval settings and
reference timestamps; the first accepted event establishes the origin.
Records sort by 20 ms block and reference stack rank. New LZ edges
determine method frequencies before compaction, and reference rank breaks
frequency ties. The separate native output codec can use different compressed
bytes while preserving decoded IDs, counts and original-renderer RGB.

The weighted sorter buffers at most 65,536 sixteen-byte entries (1 MiB), then
uses tiered fan-in-eight external runs. The LZ lookup keeps at most 262,144
new edges in memory, eight immutable sorted runs and 64 × 4 KiB read pages;
merging temporarily adds an output run. Every lookup checks the complete key.
The caches grow with use rather than reserving the maximum for small inputs.
Repeated weighted stacks can stop replaying only after a complete pass creates
no new edges. The implementation retains counts rather than expanding them
into per-sample lists.

Scratch runs are created in the owned temporary directory and immediately
unlinked on the supported Unix systems; open handles keep them usable and the
kernel reclaims them even if the worker is killed. Normal returns close every
owned spool/run. Disk use still grows with distinct external-sort records and
LZ edges; bounded RAM does not imply bounded temporary disk use.

## OTLP event ownership

The 4.5 OTLP schema preserves every accepted event timestamp. Conversion writes
fixed-size 40-byte records to an owned spool, then externally sorts by raw
thread/stack identity and event ordinal. It does not retain a per-event Go
object or timestamp list. Chunk-local stack and thread-name caches prevent
per-sample path resolution and string allocation; their budget charges are
released when the chunk ends. Leaf/root statistics are credited in constant
time, without walking every ancestor for every OTLP event.

Dictionary tables use scalar indexes and guarded arrays. An eight-way tiered
sort bounds open run metadata and merge inputs; repeated passes emit packed
timestamps/optional values directly to the output. The selected period is
patched after chunk events, so late ActiveSetting records are respected.
Retained native allocations keep their resolved paths and original timestamps.
Output size and scratch disk still grow with the number of events.

## Memory controls and publication

`--memory-limit` combines Go's soft runtime memory limit with a conservative
application guard for major data stores. Parser constant-pool growth, numeric
slabs, string arenas and main indexes are guarded before allocation. Rehashing
accounts for old and new index storage while both are live. Selected heatmap
identities and rank arrays have additional charges. The second heatmap pass
uses the remaining parser/model budget and fails when it is exhausted.

This guard is not a complete account of all maps, stacks, runtime metadata or
temporary Go allocations. Go can exceed its runtime soft limit to make
progress. Neither number is an exact hard RSS cap. See the official
[Go GC guide](https://go.dev/doc/gc-guide) and
[SetMemoryLimit contract](https://pkg.go.dev/runtime/debug#SetMemoryLimit).

With either limit flag the CLI runs conversion in a worker. The supervisor
samples actual worker RSS every 2 ms for `--max-rss`, kills an over-limit
worker, removes owned scratch and returns exit 75. Polling can overshoot and
does not include the supervisor. Go does not use a low address-space limit as
an RSS proxy. Use Linux cgroup v2 `memory.max` or a container for strict
whole-tree kernel accounting.

The worker stages complete outputs and a manifest. Publication starts only
after successful worker exit. Regular output replacement is atomic per file;
a multi-file batch is not a single filesystem transaction. Existing devices,
FIFOs and descriptor paths receive completed bytes without replacement or
truncation. Their stream writes cannot provide file replacement atomicity.

`--memory-stats` forces an opt-in GC and reports live heap, heap spans,
runtime-retained bytes and actual RSS separately. Limited multi-input workers
also return reclaimable pages between files. Allocator/runtime retention may
keep RSS higher than the live heap; a plateau must be assessed with repeated
inputs, not inferred from one heap counter.

## Package boundaries

| Package | Responsibility |
|---|---|
| `internal/jfr` | Streaming reader, bounded metadata/constant pools, reusable events, optional dictionary order |
| `internal/convert` | Compact profile model, filtering, spans, codecs, native/heatmap external sorts and reference ordering |
| `internal/cli` | Arguments, output planning, atomic publication, limits, RSS and diagnostics |
| `assets` | Embedded, unchanged upstream UI templates |
| `cmd/jfrconv` | Native command entry point and embedded time-zone data |

The converter entry points are `Run` and `RunDiff`, taking an output writer and
immutable options. Limited CLI execution supplies an owned `TempDir`.
Java/Node-based tests are development oracles and are not runtime dependencies.

## Recorded limit verification

The 0.2.0 native Linux arm64 64-input run completed with a 16 MiB soft Go
budget and a 32 MiB sampled worker RSS guard inside a strict 64 MiB Docker
cgroup, with swap disabled. Kernel `memory.peak` was **16,977,920 bytes** and
OOM counters were zero; scratch was clean afterward. Cgroup accounting includes
the process tree and charged page cache. It is not a sum of process RSS or a
universal memory bound for every input.
[Linux summary and source hashes](reports/releases/v0.2.0/linux/summary.json).

That Linux run used the preserved build snapshot. Two subsequent test-helper
amendments were executed on macOS only; native production/test source remained
byte-identical. The final source manifest and exact amendment are included
beside the original snapshot. Historical 0.1.x memory and performance reports
keep their original versions; current measurements are described in
[BENCHMARKS.md](BENCHMARKS.md).
