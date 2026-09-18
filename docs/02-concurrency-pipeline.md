# Concurrency — staged pipeline, not one flat pool

For playlist/album downloads, use a 3-stage pipeline connected by buffered
channels, each stage with its own bounded worker pool sized to what actually
limits it — not one goroutine-per-track for the whole job:

    tracks ──▶ [resolve pool] ──▶ [download pool] ──▶ [tag pool] ──▶ done

- **Resolve stage** (`--resolve-jobs`, default 8): Spotify-URL-known →
  YouTube search + scoring. Bottlenecked by round-trip latency to yt-dlp's
  search, not bandwidth, so it can run wider than the download stage
  without hurting anything.
- **Download stage** (`--jobs`, default 4): the actual bandwidth-heavy step.
  This is the real constraint — running it wider than your connection can
  sustain makes every download slower, not faster.
- **Tag stage** (fixed at 2, not user-configurable — never the bottleneck):
  CPU-light, disk I/O only.

## Implementation
- Each stage is a function taking an input channel and an output channel,
  launched N times via `errgroup.Go` with a shared `context.Context` for
  cancellation (one failed/cancelled run should stop all three stages, not
  just its own).
- Bound every channel (small buffer, e.g. `cap = concurrency`) so a slow
  download stage naturally backpressures resolve instead of resolve racing
  ahead and buffering the whole playlist in memory.
- On ctx cancellation (Ctrl+C, or a fatal error), all three pools must drain
  and exit cleanly, not leak goroutines — verify this in a test that
  cancels mid-pipeline.
- NDJSON progress lines are emitted as a track crosses each stage boundary,
  not just at the end, so `--json` consumers see
  `resolved → downloading → tagging → done` in real time rather than one
  lump at completion. See `03-communication-contract.md`.

## As built
- `internal/pipeline.Run` is generic: stages with worker counts, bounded
  channels, `errgroup` with one shared context. A stage returning `done`
  ends an item early (exists / linked); a per-item error ends only that
  item; an error the caller marks fatal (missing yt-dlp/ffmpeg) or Ctrl+C
  stops everything. `pipeline_test.go` covers routing, per-stage bounds,
  fatal stop, and a mid-flight cancel with zero leaked goroutines.
- Stages in `cmd/geet/download.go`: **resolve** (skip existing →
  Deezer tags → duplicate link → YouTube search), **download** (retries),
  **tag** (fixed 2; encode, rename into place, index).
- Before the pipeline, reading a playlist from Spotify's pages is itself
  parallel (`resolve_jobs` wide, order kept, HTTP 429 retried).
- Measured: a 50-track playlist in ~76s (was ~6 min sequentially).
