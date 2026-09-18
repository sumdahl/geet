# Future scope (v2) — do not build until plugin v1 works

Auto-detect what's currently playing via `playerctl` (already used in
`omarchy-plugin-media`) and offer a download prompt without the user having
to copy a link.

- Plugin polls/subscribes to `playerctl metadata --follow` (same pattern as
  the existing media widget), filtered to `player == spotify`.
- On track change, extract what playerctl exposes (title, artist, album,
  possibly `mpris:trackid`, which for Spotify is a `spotify:` URI —
  `spotify:track:{id}` — convert directly to
  `https://open.spotify.com/track/{id}`; no need to re-search Spotify's API
  by title/artist text).
- Show a lightweight, non-blocking prompt/OSD: "Download {artist} -
  {title}?" with accept/dismiss — it fires on every track change, not just
  ones the user wants saved, so it must be easy to ignore.
- On accept, plugin invokes `spotify-dl download <spotify-url> --json`
  exactly as the manual/clipboard path does — same engine, same NDJSON
  contract, no new engine functionality needed. This is the payoff of the
  engine/plugin split: v2 is a pure plugin-side feature.
- Worth deciding at build time: debounce so skipping through tracks doesn't
  spam prompts, and whether to remember "already asked about this track"
  for the session so replays don't re-prompt.

Flag this explicitly once plugin v1 (manual/clipboard trigger) is done and
working — don't start on it before then.
