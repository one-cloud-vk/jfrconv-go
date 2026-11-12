# Original 4.5 UI and exact heatmap shades

Version **0.2.0** embeds the unchanged HTML, CSS and JavaScript from exact
async-profiler **v4.5**, commit
`11aaea310cb75843f0e7532365d8cedc8d0e14c8`. Normal and differential flamegraphs
use its unified template. Heatmaps use its original template, decoder and
palette. This includes the 4.5 dark-mode control, D shortcut and information
legend. The converter produces the compact data those original applications
consume. [Source and executable evidence](reports/releases/v0.2.0/evidence.json).

## Method identity → ID → RGB

Heatmap shades are deterministic hashes of numeric method IDs. Go reproduces
Java's dictionary traversal, method/stack encounter ranks, new-LZ-edge
frequencies before compaction and stable ties. Numeric IDs are compared as
well as displayed identities and actual RGB values.

The sparse reference dictionary stores occupied slots and logical capacity,
including resize history across chunk clears. It applies 4.5's high-bit key
remapping before probing; raw zero is allowed and the reserved remapped-zero
key is rejected. Unused stack constants still influence traversal. A second
constant-pool-only pass assigns reference ranks without retaining event lists.
Identity includes class, method, packed line/BCI, frame kind and start role;
thread frames and allocation/lock class frames have distinct typed identities.
With `--threads`, the artificial thread frame owns the start role.

Weighted records sort by `(20 ms block, reference stack rank)`. Wall weights
are placed at their repeated timestamps using the recording's wall/interval
setting. The first accepted sample establishes the reference origin; earlier
samples and sub-millisecond timestamps retain the original clock conversion
and block placement. Method frequencies count new LZ edges before compaction.

The LZ lookup keeps at most 262,144 new edges, eight sorted runs and 256 KiB of
read pages; merging can temporarily add one run. Complete keys are verified.
Weighted repetition stops early only after an entire stack pass adds no edge.
The native output codec can produce different compressed bytes while preserving
all decoded methods, IDs, blocks, counts, locations and colors.

The shipped reference fixtures include:

| Fixture | Reference checks |
|---|---|
| [heatmap-order.jfr](tests/fixtures/heatmap-order.jfr) | Three chunks, 137 → 7 → 9 stack definitions, unused constants, capacity across clears, ties, locations and multiplicities; 11 used IDs/RGB |
| [heatmap-start.jfr](tests/fixtures/heatmap-start.jfr) | Same methods/locations in start and non-start roles; four used IDs/RGB |
| [heatmap-wall45.jfr](tests/fixtures/heatmap-wall45.jfr) | ActiveSetting, weighted wall events, earlier timestamps, fractional milliseconds, thread frames and a filtered first sample |

Tests also force LZ spill/merge and external weighted runs, distinguish native
frames with identical displayed names, and compare large weighted repetitions
with the unspilled reference.

## Fresh 0.2.0 comparisons

The corpus contains **24 Java/native page pairs**. Its **21 flamegraph pairs**
execute the original application; three heatmap entries receive seeded copies
only. All **21 HTML/CSS shells** match byte-for-byte after complete script
elements are removed. Original JavaScript is tested separately.
[Corpus](reports/releases/v0.2.0/ui/manifest.json),
[static shells](reports/releases/v0.2.0/ui/static-shell.json).

At **each of DPR 1 and DPR 2**, all **357 interaction phases** have identical
state and draw calls, and **882 assertions** pass across Java/native pages.
The original 273 phases/714 assertions remain, covering tooltips, selection,
zoom, inversion, search, navigation, reset and mouseout. The extra 84 phases/
168 assertions cover the new dark-mode button, D shortcut and legend hover.
The unchanged theme prelude and main application scripts both execute.
[DPR 1](reports/releases/v0.2.0/ui/renderer-dpr1.json),
[DPR 2](reports/releases/v0.2.0/ui/renderer-dpr2.json).

The separate final [heatmap report](reports/releases/v0.2.0/ui/heatmap-colors.json)
contains **22 exact comparisons**: ten CPU/wall/allocation/lock modes, nine
order/start/thread/official-Span probes and three weighted-wall/first-time
probes. Every case matches used numeric method IDs, original-renderer RGB and
the decoded timeline. Empty cases remain explicitly represented. Additional
[official Span and Unicode comparisons](reports/releases/v0.2.0/official-span/final-oracles.json)
cover all six formats, including heatmaps with literal Unicode tag filters.

The final executable, input hashes, Java jar and commands are retained in
compact JSON. Generated/seeded HTML remains in the recorded work directory;
it is not duplicated into the release evidence bundle. Initial host and
fixture setup failures are retained. The shipped renderer now provides the
actual 4.5 DOM APIs while retaining all previous assertions; the validator
uses a valid weighted-wall fixture. Only those two helper files changed after
the build. [Helper amendment](reports/releases/v0.2.0/helper-amendment.json).

```sh
./bin/jfrconv --cpu -o heatmap recording.jfr go.html
# Produce java.html using the same input/options and exact Java 4.5.
node tools/heatmap_colors.js java.html go.html
node tools/ui_renderer_parity.js --java java-flame.html --go go-flame.html \
  --output work/ui-parity --strict
```

The color tool reads encoded data and executes only the bundled upstream
functions, never scripts supplied by input pages. It compares used semantic
identity, numeric ID and RGB separately and fails on a difference.

## Scope and historical boundaries

The renderer uses deterministic `Math.random()` and DOM/canvas mocks. It checks
application state, geometry, draw calls and original handlers; it is not a
browser layout engine. Heatmap decoder/color checks do not claim a complete
real-browser heatmap interaction matrix. Font rasterization and layout depend
on the actual browser environment. Production flamegraph colors retain the
original random variation between loads.

Rare treeified Java `HashMap` ordering for equal-name siblings and an
existing-key resize exactly at the capacity threshold are not fully emulated.
Go also preserves native-leak paths resolved in their owning chunk, instead
of resolving retained old raw IDs against unrelated final-chunk definitions.
These specific boundaries do not permit ordinary heatmap IDs or shades to
vary.

The [0.1.1](reports/releases/v0.1.1/evidence.json) and
[0.1.0](reports/releases/v0.1.0/evidence.json) UI evidence remains unchanged,
with its original 4.1 reference and executable hashes. The 4.1 extra-stack
cache alias exceptions documented there are historical: 4.5 constructs typed
extra frames per chunk differently. Earlier Rust screenshots and binary hashes
are not presented as Go 0.2.0 evidence.
