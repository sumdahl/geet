package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sumdahl/geet/internal/library"
)

// Setting is one configurable value. Its flag and environment variable names
// derive from Key, so adding a field to Settings is all it takes to expose it
// everywhere.
type Setting struct {
	Key    string // dotted TOML key, e.g. "youtube.search_results"
	Usage  string
	Secret bool
	ptr    any
}

// Settings lists every setting, pointing into c.
func (c *Config) Settings() []Setting {
	return []Setting{
		{Key: "output", Usage: "music library directory", ptr: &c.Output},
		{Key: "output_template", Usage: "file path under output, without extension; placeholders: " + libraryPlaceholders(), ptr: &c.OutputTemplate},
		{Key: "playlist_folder", Usage: "put a playlist's tracks in a folder named after it, inside output", ptr: &c.PlaylistFolder},
		{Key: "playlist_folder_case", Usage: "playlist folder letter case: lower (road-trip-mix), capitalize (Road-trip-mix) or title (Road-Trip-Mix)", ptr: &c.PlaylistFolderCase},
		{Key: "format", Usage: "audio format: opus, flac or mp3", ptr: &c.Format},
		{Key: "bitrate", Usage: `audio bitrate such as 320k; empty means best available`, ptr: &c.Bitrate},
		{Key: "overwrite", Usage: "re-download tracks whose file already exists instead of skipping them", ptr: &c.Overwrite},
		{Key: "duplicates", Usage: "a track already downloaded elsewhere (another playlist, or the same recording on another release): link (hard link, no extra space), copy, skip, or download again", ptr: &c.Duplicates},
		{Key: "index_path", Usage: "file remembering every downloaded track, for duplicates (default $XDG_DATA_HOME/geet/index.json)", ptr: &c.IndexPath},
		{Key: "progress", Usage: "animated progress bars: auto (only in a terminal, and not with --json), always or never", ptr: &c.Progress},
		{Key: "download_retries", Usage: "extra attempts when a YouTube download fails (it is often a temporary 403 or throttling)", ptr: &c.DownloadRetries},
		{Key: "jobs", Usage: "concurrent downloads", ptr: &c.Jobs},
		{Key: "resolve_jobs", Usage: "songs read from Spotify and looked up on YouTube at once; higher reads big playlists faster but makes Spotify rate-limit sooner", ptr: &c.ResolveJobs},
		{Key: "spotify.client_id", Usage: "Spotify Web API client ID (optional; needs Premium)", ptr: &c.Spotify.ClientID},
		{Key: "spotify.client_secret", Usage: "Spotify Web API client secret", Secret: true, ptr: &c.Spotify.ClientSecret},
		{Key: "spotify.cache_days", Usage: "days to remember songs read from Spotify's public pages, so reading a playlist again skips them; 0 turns the cache off", ptr: &c.Spotify.CacheDays},
		{Key: "youtube.search_query", Usage: "YouTube search text; placeholders: {artists} {artist} {title} {album}", ptr: &c.YouTube.SearchQuery},
		{Key: "youtube.fallback_query", Usage: "second search when nothing from search_query matches (same placeholders); empty disables", ptr: &c.YouTube.FallbackQuery},
		{Key: "youtube.search_results", Usage: "YouTube results to score per track", ptr: &c.YouTube.SearchResults},
		{Key: "youtube.max_duration_diff", Usage: "reject YouTube results whose length differs from Spotify's by more than this", ptr: &c.YouTube.MaxDurationDiff},
		{Key: "youtube.cookies_file", Usage: "Netscape cookies file passed to yt-dlp", ptr: &c.YouTube.CookiesFile},
		{Key: "youtube.cookies_from_browser", Usage: "send your YouTube sign-in from a browser, to get past \"confirm you're not a bot\": auto (the default browser), or brave, chromium, chrome, firefox, …; the keyring is added automatically. Off by default: YouTube then sees downloads as your account's", ptr: &c.YouTube.CookiesFromBrowser},
		{Key: "youtube.music_fallback", Usage: "when YouTube search finds no match, look for the song on YouTube Music, which lists official studio audio at the album's exact length (about 4s more for that song only)", ptr: &c.YouTube.MusicFallback},
		{Key: "youtube.title_fallback", Usage: "last resort when nothing else matches: search by the song's title alone and accept only an upload with the full title within 2s of its length (for artists YouTube knows by another name); the song then carries a warning", ptr: &c.YouTube.TitleFallback},
		{Key: "youtube.extra_args", Usage: "extra yt-dlp arguments, space-separated", ptr: &c.YouTube.ExtraArgs},
		{Key: "search.country", Usage: "iTunes store that geet search looks in (two letters, e.g. US, GB, IN)", ptr: &c.Search.Country},
		{Key: "search.limit", Usage: "search results offered to pick from", ptr: &c.Search.Limit},
		{Key: "search.picker", Usage: "how search results are picked: auto (fzf if installed), fzf or list (numbered prompt)", ptr: &c.Search.Picker},
		{Key: "search.confirm", Usage: "ask before downloading songs picked from the search menu (--pick never asks)", ptr: &c.Search.Confirm},
		{Key: "player.engine", Usage: "what geet play uses for audio: auto (mpv when installed, else ffplay), mpv or ffplay. Only mpv can seek and report an exact position, which synced lyrics need", ptr: &c.Player.Engine},
		{Key: "player.stream", Usage: "play a song that isn't downloaded straight from YouTube, so it starts in seconds; \"d\" while it plays keeps a proper copy. Off downloads first, as before", ptr: &c.Player.Stream},
		{Key: "player.visualizer", Usage: "show the spectrum beside the song while it plays", ptr: &c.Player.Visualizer},
		{Key: "player.lyrics", Usage: "look up synced lyrics (LRCLIB) and follow them while the song plays; songs without lyrics simply show none", ptr: &c.Player.Lyrics},
		{Key: "player.shuffle", Usage: "play a queue in random order", ptr: &c.Player.Shuffle},
		{Key: "player.repeat", Usage: "start the queue again when it ends instead of quitting", ptr: &c.Player.Repeat},
		{Key: "player.mpv", Usage: "mpv executable, used by geet play", ptr: &c.Player.MPV},
		{Key: "player.ffplay", Usage: "ffplay executable (ships with ffmpeg), the fallback player", ptr: &c.Player.FFplay},
		{Key: "trending.source", Usage: "where geet trending reads the chart: auto (Deezer, topped up from Apple), deezer (localised by your connection) or apple (your search.country store)", ptr: &c.Trending.Source},
		{Key: "trending.cache_for", Usage: "how long a chart is kept before geet reads it again; charts move slowly, and this keeps the panel instant", ptr: &c.Trending.CacheFor},
		{Key: "watch.interval", Usage: "how often geet watch checks the clipboard for a new link", ptr: &c.Watch.Interval},
		{Key: "watch.notify", Usage: "geet watch shows a desktop notification (with the cover) when a link starts, finishes or fails", ptr: &c.Watch.Notify},
		{Key: "tools.yt_dlp", Usage: "yt-dlp executable", ptr: &c.Tools.YtDlp},
		{Key: "tools.ffmpeg", Usage: "ffmpeg executable", ptr: &c.Tools.FFmpeg},
		{Key: "tools.ffprobe", Usage: "ffprobe executable, used to index a library downloaded before the index existed", ptr: &c.Tools.FFprobe},
		{Key: "tools.wl_paste", Usage: "wl-paste executable (wl-clipboard), which geet watch reads the clipboard with", ptr: &c.Tools.WlPaste},
		{Key: "tools.notify_send", Usage: "notify-send executable (libnotify), for geet watch's notifications", ptr: &c.Tools.NotifySend},
	}
}

func (s Setting) Flag() string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(s.Key)
}

func (s Setting) Env() string {
	return "GEET_" + strings.ToUpper(strings.ReplaceAll(s.Key, ".", "_"))
}

func (s Setting) Set(raw string) error {
	switch p := s.ptr.(type) {
	case *string:
		*p = raw
	case *bool:
		v, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("%s: %q is not true or false", s.Key, raw)
		}
		*p = v
	case *int:
		v, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("%s: %q is not a whole number", s.Key, raw)
		}
		*p = v
	case *Duration:
		if err := p.UnmarshalText([]byte(strings.TrimSpace(raw))); err != nil {
			return fmt.Errorf("%s: %q is not a duration like 10s", s.Key, raw)
		}
	case *[]string:
		*p = strings.Fields(raw)
	default:
		panic(fmt.Sprintf("config: setting %s has unsupported type %T", s.Key, s.ptr))
	}
	return nil
}

// Type names the value's kind for tools that build a settings UI.
func (s Setting) Type() string {
	switch s.ptr.(type) {
	case *bool:
		return "bool"
	case *int:
		return "int"
	case *Duration:
		return "duration"
	case *[]string:
		return "list"
	}
	return "string"
}

// String renders the current value the way Set accepts it.
func (s Setting) String() string {
	switch p := s.ptr.(type) {
	case *string:
		return *p
	case *bool:
		return strconv.FormatBool(*p)
	case *int:
		return strconv.Itoa(*p)
	case *Duration:
		return p.String()
	case *[]string:
		return strings.Join(*p, " ")
	}
	return ""
}

func libraryPlaceholders() string {
	return strings.Join(library.Placeholders, " ")
}
