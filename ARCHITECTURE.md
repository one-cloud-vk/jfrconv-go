# Architecture

The converter is built with `CGO_ENABLED=0` and uses only the Go standard
library. Java and Node are used by reference tests, not by conversion.

| Package | Responsibility |
|---|---|
| `internal/jfr` | Streaming JFR reader, metadata, constant pools and reusable events |
| `internal/convert` | Profile model, filters, codecs and external sorting |
| `internal/cli` | Arguments, output planning, memory limits and worker supervision |
| `assets` | Embedded upstream UI templates |
| `cmd/jfrconv` | Command entry point and embedded time-zone data |

## Input and model ownership

The reader uses a 128 KiB buffer rather than loading the recording. Constant
pools belong to the current chunk and are replaced at chunk boundaries.
Events are reused callback values; consumers resolve IDs while that chunk is
alive. A bounded table retains the six sampling-period settings across chunks.

Stack traces use dense slabs with separate method, location and kind columns:
13 bytes per ordinary frame, plus allocator/slab overhead. Wide method IDs
use an optional high-word column. Profile names are interned in an arena;
frames and prefix nodes use numeric IDs and scalar indexes to reduce GC work.
Chunk-local caches reuse path resolution buffers across samples.

Each conversion owns its reader, model and scratch files, and releases them
before processing the next input. Span filtering scans merged per-thread
intervals in a second event pass. Memory still depends on the largest constant
pool, distinct output paths, selected intervals and retained native allocations.

## Disk-backed processing

Native leak analysis sorts allocations/frees on disk. Heatmap processing
reconstructs Java dictionary/encounter order, then sorts bounded records to
preserve method IDs and colors. Its LZ lookup uses bounded caches and sorted
runs instead of retaining every edge in a Go map.

OTLP preserves every accepted event timestamp. Fixed-size 40-byte spool records
are externally grouped by raw thread/stack identity and event ordinal. An
eight-way tiered merge bounds run metadata; timestamps and values stream to
encoded output. No per-event timestamp list remains in memory.

Scratch files are created in the conversion's temporary directory and unlinked
immediately on supported Unix systems. Open handles keep them usable; closing
the process reclaims them. Memory use follows the working sets above;
temporary disk use can grow with the event/output size.

## Limits and output

`--memory-limit` combines guards for major parser/model stores with Go's soft
runtime memory limit. Resizing indexes charges both old and new storage while
they coexist. The guard does not account for every map, stack or temporary
allocation, and Go may exceed its soft target to make progress.

With either limit flag, the CLI launches its own worker. `--max-rss` samples
worker RSS every 2 ms and kills it on an observed excess. Polling can overshoot;
supervisor memory is separate. A strict whole-process-tree limit requires a
Linux cgroup or container limit.

A limited worker stages the whole batch before publishing. Limit denial returns
75 and removes owned scratch. Regular output replacement is atomic per file,
not across the batch. Existing devices, FIFOs and descriptor outputs receive
completed bytes through their handles; stream writes are not atomic file
replacement.

`--memory-stats` forces a diagnostic GC after each input and reports live heap,
runtime-retained memory and actual RSS separately. Limited multi-input workers
also return reclaimable pages between files. Runtime retention can keep RSS
above the live heap size.
