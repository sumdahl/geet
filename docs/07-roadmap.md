# Deliverables, in order — pause for review after each

1. Repo scaffold + config loading + Spotify client with one passing test.
2. YouTube resolver + scoring against fixture JSON.
3. Download + tag pipeline, `--json`/NDJSON output (single-track, no
   concurrency yet), tested end-to-end on one real public-domain track.
   **Engine is done and usable standalone from any terminal here — stop and
   let the user use it directly before touching concurrency or the plugin.**
4. Staged concurrency pipeline for playlists (`02-concurrency-pipeline.md`).
5. Clipboard `watch` daemon streaming NDJSON.
6. Omarchy plugin v1 (`04-plugin-spec.md`).
7. PKGBUILD + README.
8. (Later, separately) playerctl auto-detect v2 (`05-future-v2-playerctl.md`).
9. (Later, only if Mac users ask) `geet watch` on macOS: read the clipboard
   with `pbpaste` in `internal/clipboard`, and notify through
   `terminal-notifier` when installed (cover art via `-contentImage`,
   in-place update via `-group`), falling back to `osascript` `display
   notification` (text only, shown as "Script Editor"). A native
   notification API isn't an option: Apple's needs a signed app bundle.
   There's no Mac to test on, so a real run on one is required before
   calling it done.
