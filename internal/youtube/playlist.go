package youtube

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
)

// RefPrefix marks a track known only by its YouTube video: "youtube:<id>".
const RefPrefix = "youtube:"

// PlaylistID returns the list id of a YouTube or YouTube Music playlist
// link. A watch link that happens to carry a list (a song played from a
// playlist) is not one: the user copied a song, not the list.
func PlaylistID(link string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	switch host {
	case "youtube.com", "music.youtube.com", "m.youtube.com":
	default:
		return "", false
	}
	id := u.Query().Get("list")
	return id, u.Path == "/playlist" && id != ""
}

// WatchURL is the YouTube Music page of a video, which is what a playlist
// song's file records as its source.
func WatchURL(id string) string {
	return "https://music.youtube.com/watch?v=" + id
}

// VideoID returns the video in a song link: a youtube.com or
// music.youtube.com watch link (a list in it is ignored: the user copied
// the song), youtu.be/<id> or youtube.com/shorts/<id>.
func VideoID(link string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", false
	}
	var id string
	switch host := strings.TrimPrefix(strings.ToLower(u.Host), "www."); {
	case host == "youtu.be":
		id = strings.Trim(u.Path, "/")
	case host != "youtube.com" && !strings.HasSuffix(host, ".youtube.com"):
		return "", false
	case u.Path == "/watch":
		id = u.Query().Get("v")
	default:
		id, _ = strings.CutPrefix(u.Path, "/shorts/")
		if id == u.Path {
			return "", false
		}
	}
	return id, id != "" && !strings.Contains(id, "/")
}

// Playlist lists a playlist's videos as tracks, with what their titles and
// channels say: good enough to find each song in a catalog, which is where
// proper tags come from. --flat-playlist reads the list without opening a
// single video, so even a few hundred songs take a couple of seconds.
func (r *Resolver) Playlist(ctx context.Context, link string) (spotify.Collection, error) {
	id, ok := PlaylistID(link)
	if !ok {
		return spotify.Collection{}, fmt.Errorf("not a YouTube playlist link: %q", link)
	}
	// youtube.com, not music.youtube.com: yt-dlp only redirects the latter
	// there anyway, with a warning.
	out, err := r.opts.YtDlp.Run(ctx, "--flat-playlist", "--dump-single-json", "--no-warnings", "--no-progress",
		"https://www.youtube.com/playlist?list="+id)
	if err != nil {
		return spotify.Collection{}, fmt.Errorf("%w (reading playlist %s)", err, id)
	}
	return parsePlaylist(id, out)
}

func parsePlaylist(id string, raw []byte) (spotify.Collection, error) {
	var p struct {
		Title   string `json:"title"`
		Entries []struct {
			ID       string  `json:"id"`
			Title    string  `json:"title"`
			Channel  string  `json:"channel"`
			Uploader string  `json:"uploader"`
			Duration float64 `json:"duration"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return spotify.Collection{}, fmt.Errorf("parsing yt-dlp playlist output: %w", err)
	}
	col := spotify.Collection{Ref: spotify.Ref{Kind: spotify.KindPlaylist, ID: id}, Name: p.Title}
	for _, e := range p.Entries {
		// Deleted and private videos stay in a playlist as placeholders
		// with no length.
		if e.ID == "" || e.Duration == 0 {
			continue
		}
		col.Tracks = append(col.Tracks, videoTrack(e.ID, FromVideo(e.Title, cmp.Or(e.Channel, e.Uploader)), e.Duration))
	}
	return col, nil
}

// videoTrack fills in what every song read off a video carries.
func videoTrack(id string, t spotify.Track, seconds float64) spotify.Track {
	t.ID = RefPrefix + id
	t.SourceURL = WatchURL(id)
	t.Duration = time.Duration(seconds * float64(time.Second))
	t.CoverURL = "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"
	return t
}

// Video reads one song link as a one-song collection. A single video's
// details, unlike a playlist's listing, include YouTube Music's own song
// metadata (track, artists, album, year) when the upload is a song, which
// beats anything read off the title.
func (r *Resolver) Video(ctx context.Context, link string) (spotify.Collection, error) {
	id, ok := VideoID(link)
	if !ok {
		return spotify.Collection{}, fmt.Errorf("not a YouTube song link: %q", link)
	}
	out, err := r.opts.YtDlp.Run(ctx, "--dump-single-json", "--no-playlist", "--skip-download", "--no-warnings", "--no-progress",
		"https://www.youtube.com/watch?v="+id)
	if err != nil {
		return spotify.Collection{}, fmt.Errorf("%w (reading video %s)", err, id)
	}
	return parseVideo(out)
}

func parseVideo(raw []byte) (spotify.Collection, error) {
	var v struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Channel     string   `json:"channel"`
		Uploader    string   `json:"uploader"`
		Duration    float64  `json:"duration"`
		Track       string   `json:"track"`
		Artists     []string `json:"artists"`
		Artist      string   `json:"artist"` // older yt-dlp: the artists joined by ", "
		Album       string   `json:"album"`
		ReleaseYear int      `json:"release_year"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return spotify.Collection{}, fmt.Errorf("parsing yt-dlp video output: %w", err)
	}
	if v.ID == "" {
		return spotify.Collection{}, errors.New("yt-dlp returned no video")
	}
	t := FromVideo(v.Title, cmp.Or(v.Channel, v.Uploader))
	artists := v.Artists
	if len(artists) == 0 && v.Artist != "" {
		artists = splitArtists(v.Artist)
	}
	if v.Track != "" && len(artists) > 0 {
		t = spotify.Track{Title: v.Track, Artists: artists, AlbumArtist: artists[0], Album: v.Album, Year: v.ReleaseYear}
	}
	t = videoTrack(v.ID, t, v.Duration)
	return spotify.Collection{Ref: spotify.Ref{Kind: spotify.KindTrack, ID: t.ID}, Name: t.Title, Tracks: []spotify.Track{t}}, nil
}

var (
	// "(feat. X)", "[ft. X]", "(with X)": a credit in brackets.
	bracketFeat = regexp.MustCompile(`(?i)\s*[(\[]\s*(?:ft|feat|featuring|with)\b\.?\s+([^()\[\]]+)[)\]]`)
	// "... ft. X" to the end of the part it's in.
	bareFeat  = regexp.MustCompile(`(?i)\s+(?:ft|feat|featuring)\b\.?\s+`)
	bracketed = regexp.MustCompile(`\s*[(\[][^()\[\]]*[)\]]`)
	// Unbracketed leftovers such as "... OFFICIAL MUSIC VIDEO".
	videoWords = regexp.MustCompile(`(?i)\s+(?:official\s+)?(?:music\s+|lyrics?\s+)?(?:video|audio|visuali[sz]er)\b.*$`)
	artistSep  = regexp.MustCompile(`\s*(?:,|&|\s[xX]\s)\s*`)
)

// FromVideo reads a song's title and artists from a video's title and
// channel, as uploads name them: "Artist - Song (Official Video)",
// "A, B - Song ft. C", or just "Song" on the artist's own channel. Brackets
// are dropped unless they name a version ("(Remix)", "(Live)"), because
// catalogs title that version so too.
func FromVideo(title, channel string) spotify.Track {
	// "Song | Film | Singer | Label": the rest is credits. But the
	// channel's own name first is "Artist | Song".
	parts := strings.Split(title, " | ")
	title = parts[0]
	if len(parts) > 1 && strings.EqualFold(strings.TrimSpace(parts[0]), channelArtist(channel)) {
		title = parts[0] + " - " + parts[1]
	}

	var featured []string
	for _, m := range bracketFeat.FindAllStringSubmatch(title, -1) {
		featured = append(featured, splitArtists(m[1])...)
	}
	title = bracketFeat.ReplaceAllString(title, "")
	title = bracketed.ReplaceAllStringFunc(title, func(b string) string {
		if len(textnorm.Variants(b, "")) > 0 {
			return b
		}
		return ""
	})
	title = strings.TrimSpace(videoWords.ReplaceAllString(title, ""))

	var artists []string
	for _, sep := range []string{" - ", " – ", " — "} {
		if a, song, ok := strings.Cut(title, sep); ok && strings.TrimSpace(song) != "" {
			a, f := cutFeat(a)
			artists, featured = splitArtists(a), append(featured, f...)
			title = song
			break
		}
	}
	title, f := cutFeat(title)
	featured = append(featured, f...)
	if len(artists) == 0 {
		artists = []string{channelArtist(channel)}
	}
	for _, f := range featured {
		if !containsFold(artists, f) {
			artists = append(artists, f)
		}
	}
	return spotify.Track{Title: strings.TrimSpace(title), Artists: artists, AlbumArtist: artists[0]}
}

// cutFeat splits "Song ft. A ft. A" into "Song" and its credited artists.
func cutFeat(s string) (string, []string) {
	parts := bareFeat.Split(s, -1)
	var featured []string
	for _, p := range parts[1:] {
		featured = append(featured, splitArtists(p)...)
	}
	return strings.TrimSpace(parts[0]), featured
}

func splitArtists(s string) []string {
	var out []string
	for _, a := range artistSep.Split(s, -1) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// channelArtist turns "Taylor Swift - Topic" or "TaylorSwiftVEVO" into the
// artist's name, as near as the channel allows.
func channelArtist(channel string) string {
	channel = strings.TrimSuffix(channel, " - Topic")
	channel = strings.TrimSuffix(channel, "VEVO")
	return strings.TrimSpace(channel)
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
