# Original UI and exact heatmap shades

The flamegraph and heatmap pages embed the unchanged async-profiler **4.1**
HTML, CSS and JavaScript. Labels, controls, palettes, mouse actions, keyboard
handlers and the original browser decoders come from upstream. The converter
writes the formats those decoders consume. Differential graphs use the
unchanged newer public upstream diff template; they are an explicit extension
to the 4.1 target.

## Method identity → ID → RGB

Heatmap shades are deterministic hashes of numeric method IDs. Go reproduces
Java's dictionary traversal, method/stack encounter ranks, new-LZ-edge
frequencies before compaction and stable ties. This preserves the actual used
method IDs, rather than hiding an ID difference behind normalized names.

Constant-pool ordering is sparse: only occupied dictionary slots are stored,
while Java's resize history and logical capacity survive chunk clears.
Unused stack definitions still influence traversal correctly. The second
constant-pool-only pass assigns reference ranks without retaining event lists.
Method identity includes class, method, packed line/BCI, frame kind and
start/non-start role, even when two distinct identities display the same name.

The external weighted sorter replays `(20 ms block, reference stack rank)`
order. A capped LZ index spills complete keys to sorted runs while preserving
edge IDs; it retains at most 262,144 new edges, eight runs and 256 KiB of read
pages. Merging can temporarily add one run. Weighted repetition stops early
only when a complete stack pass adds no new edge. The output codec shares
prefixes; its compressed bytes need not be identical to Java's to preserve
the decoded profile, method IDs and colors.

The regression test compares recorded Java goldens using the original
`getColorStable` function:

| Fixture | Reference checks |
|---|---|
| [heatmap-order.jfr](tests/fixtures/heatmap-order.jfr) | Three chunks with 137 → 7 → 9 stack definitions, unused constants, capacity across clear, ties, locations and multiplicities; [11 used-method IDs/RGB](tests/fixtures/heatmap-order.colors.json) |
| [heatmap-start.jfr](tests/fixtures/heatmap-start.jfr) | The same methods/locations in start and non-start roles; [4 used-method IDs/RGB](tests/fixtures/heatmap-start.colors.json) |

Separate tests force LZ spill/merge and external weighted runs, preserve
identically displayed native frames from distinct classes, and compare large
weighted repetition with the unspilled reference.

Same-input Java/Go comparisons matched all used IDs/RGB on the mixed fixture
(5 methods), real async-profiler CPU (756), allocation (119) and lock (6)
recordings, and a JDK CPU recording (99). These are bounded functional checks.
They establish neither a performance ratio nor arbitrary-browser pixel
equality.

```sh
./bin/jfrconv --cpu -o heatmap recording.jfr go.html
# Generate java.html from the same input/options with Java 4.1, then:
node tools/heatmap_colors.js java.html go.html
```

The color tool extracts only encoded profile data from input pages and uses
the bundled upstream decoder/color functions. It does not execute scripts
supplied by those pages. It compares used semantic identity, numeric ID and
actual RGB separately and exits nonzero on a difference.

## Version 0.1.1 UI checks

The [versioned evidence](reports/releases/v0.1.1/evidence.json) identifies the
frozen 0.1.1 executable, source and reference jar. These are fresh JDK 21
checks. The corpus has **24 page pairs**: **21
flamegraph pairs** execute the original application, while three heatmap
entries receive seeded copies only. All **21 static HTML/CSS shell comparisons** match. At each
of DPR 1 and DPR 2, **273 interaction phases** have identical draw calls/state
and **714 assertions** pass across the Java/native pages. These checks include
tooltips, zoom, inversion, search, navigation, reset and original handlers.
[Static shells](reports/releases/v0.1.1/ui/static-shell.json),
[DPR 1](reports/releases/v0.1.1/ui/renderer-dpr1.json),
[DPR 2](reports/releases/v0.1.1/ui/renderer-dpr2.json).

The final [heatmap comparison](reports/releases/v0.1.1/ui/heatmap-colors.json)
covers **10 modes: eight nonempty and two empty**. All used method IDs/RGB and
semantic stack/timeline counts match. Reports retain input hashes and case
names. Compact report JSON is included; generated and seeded HTML referenced
inside the renderer records remains in the original test work directory.
Current shell checks compare HTML/CSS after removing complete script elements;
the original JavaScript is exercised separately by the renderer. The initial
missing-page harness error and a separate default-selection JDK diagnostic are
retained in the evidence package; neither required a production change.

The previous [0.1.0 UI evidence](reports/releases/v0.1.0/evidence.json) retains
its original executable hashes and reports. It is not relabeled as a run of
0.1.1. Compact stack columns and reusable conversion scratch in 0.1.1 preserve
the same typed method identities and reference ordering.

## Explicit limits

Java 4.1 has incorrect extra-stack aliases when raw IDs repeat across chunks
or allocation identity changes between TLAB/outside-TLAB. Its native-leak
grouping can also conflate reused raw stack IDs. Go preserves the correct
methods, kinds and resolved paths instead of copying the corrupted reference
graph. These are specific legacy incorrect-alias exceptions; ordinary method
IDs are not allowed to vary as a blanket compatibility exception.

Production flamegraph shades use the original `Math.random()` and naturally
vary between page loads. Deterministically seeded test copies can compare
draw calls and interactions. Java's rare treeified equal-name sibling bucket
ordering and existing-key resize threshold behavior are not fully emulated.
Font rasterization and browser layout remain environment-dependent.

`tools/ui_renderer_parity.js` compares the full generated flamegraph app with
a deterministic random sequence and a DOM/canvas host. It checks geometry,
draw calls, tooltips and original event handlers; it is not a browser layout
engine. Use controlled generated comparison pages:

```sh
node tools/ui_renderer_parity.js --java java.html --rust go.html \
  --output work/ui-parity --strict
```

The historical `--rust` argument names the native comparison page and also
accepts Go output. Heatmap color/decoder checks use the dedicated tool above;
they do not imply that every heatmap interaction was tested in a real browser.
No earlier Rust browser screenshot or binary hash is presented as Go release
evidence. The checks above execute JavaScript with DOM/canvas mocks; a complete
real-browser interaction and font/layout matrix is separate work.
