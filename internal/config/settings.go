package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sumdahl/spotify-dl/internal/library"
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
		{Key: "index_path", Usage: "file remembering every downloaded track, for duplicates (default $XDG_DATA_HOME/spotify-dl/index.json)", ptr: &c.IndexPath},
		{Key: "progress", Usage: "animated progress bars: auto (only in a terminal, and not with --json), always or never", ptr: &c.Progress},
		{Key: "download_retries", Usage: "extra attempts when a YouTube download fails (it is often a temporary 403 or throttling)", ptr: &c.DownloadRetries},
		{Key: "jobs", Usage: "concurrent downloads", ptr: &c.Jobs},
		{Key: "resolve_jobs", Usage: "concurrent YouTube lookups", ptr: &c.ResolveJobs},
		{Key: "spotify.client_id", Usage: "Spotify Web API client ID (optional; needs Premium)", ptr: &c.Spotify.ClientID},
		{Key: "spotify.client_secret", Usage: "Spotify Web API client secret", Secret: true, ptr: &c.Spotify.ClientSecret},
		{Key: "youtube.search_query", Usage: "YouTube search text; placeholders: {artists} {artist} {title} {album}", ptr: &c.YouTube.SearchQuery},
		{Key: "youtube.search_results", Usage: "YouTube results to score per track", ptr: &c.YouTube.SearchResults},
		{Key: "youtube.max_duration_diff", Usage: "reject YouTube results whose length differs from Spotify's by more than this", ptr: &c.YouTube.MaxDurationDiff},
		{Key: "youtube.cookies_file", Usage: "Netscape cookies file passed to yt-dlp", ptr: &c.YouTube.CookiesFile},
		{Key: "youtube.cookies_from_browser", Usage: "browser yt-dlp reads cookies from, e.g. firefox", ptr: &c.YouTube.CookiesFromBrowser},
		{Key: "youtube.extra_args", Usage: "extra yt-dlp arguments, space-separated", ptr: &c.YouTube.ExtraArgs},
		{Key: "tools.yt_dlp", Usage: "yt-dlp executable", ptr: &c.Tools.YtDlp},
		{Key: "tools.ffmpeg", Usage: "ffmpeg executable", ptr: &c.Tools.FFmpeg},
		{Key: "tools.ffprobe", Usage: "ffprobe executable, used to index a library downloaded before the index existed", ptr: &c.Tools.FFprobe},
	}
}

func (s Setting) Flag() string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(s.Key)
}

func (s Setting) Env() string {
	return "SPOTIFY_DL_" + strings.ToUpper(strings.ReplaceAll(s.Key, ".", "_"))
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
