# Omarchy Plugin (v1) — build after the engine is done and proven standalone

- Thin Quickshell-side wrapper that spawns `spotify-dl watch --json` (or
  `download --json` per-URL) as a subprocess, parses the NDJSON stream (see
  `03-communication-contract.md`), and drives notifications/OSD. No engine
  logic lives in the plugin.
- Package as a PKGBUILD matching the existing `omarchy-plugin-nepse` and
  `omarchy-plugin-media` repos — get their repo structure as a reference
  before scaffolding this, rather than guessing the convention.
- Example Hyprland keybind for people who don't want the daemon running:
  `SUPER+SHIFT+Y` → `spotify-dl download "$(wl-paste)" --json`.
- Repo: `omarchy-plugin-spotify-dl` (see `00-overview.md` for naming/paths).
