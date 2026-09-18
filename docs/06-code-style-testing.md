# Code style & testing

## Code style
- No unnecessary or restating-the-obvious comments. Comment only where the
  "why" isn't self-evident from the code itself (e.g. a non-obvious
  workaround, a YouTube/Spotify API quirk).
- Idiomatic, modern-standard Go:
  - stdlib `slog` for logging
  - `errors.Is`/`errors.As` and wrapped errors (`fmt.Errorf("...: %w", err)`)
    over ad-hoc error strings
  - `context.Context` threaded through anything doing I/O (HTTP calls,
    subprocess calls, worker pool cancellation)
  - `any` over `interface{}`
  - generics only where they actually remove duplication
  - table-driven tests
  - `errgroup` for the concurrent pipeline stages
  - avoid needless abstraction/interfaces for single-implementation code

## Testing
- Table-driven tests for URL parsing and the YouTube scoring function
  (fixture JSON, no live calls).
- Tag round-trip test: write tags, read them back, assert equal.
- Pipeline cancellation test: cancel mid-flight, assert all goroutines exit
  and no leaks (see `02-concurrency-pipeline.md`).
- No live-network tests in CI — mock the Spotify HTTP client, stub yt-dlp's
  JSON output.
