package main

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
	"github.com/sumdahl/geet/internal/youtube"
	"github.com/sumdahl/geet/internal/ytdlp"
)

// readYouTube reads a YouTube or YouTube Music link: a playlist, or one
// song. Its songs carry only what the videos say; each is looked up in the
// catalogs as it reaches the resolve stage (songFinder), so downloads start
// at once instead of after a lookup per song.
func readYouTube(ctx context.Context, cfg config.Config, rep *reporter, link string) (spotify.Collection, error) {
	src, err := ytdlp.CookieSource(cfg.YouTube.CookiesFromBrowser, ytdlp.SystemProbes())
	if err != nil {
		return spotify.Collection{}, err
	}
	yt := youtube.New(youtube.Options{YtDlp: ytdlp.Runner{
		Binary:             cfg.Tools.YtDlp,
		Prefix:             cfg.Tools.YtDlpArgs,
		CookiesFile:        cfg.YouTube.CookiesFile,
		CookiesFromBrowser: src,
		ExtraArgs:          cfg.YouTube.ExtraArgs,
	}})
	if _, ok := youtube.PlaylistID(link); ok {
		ph := rep.ui.phase("Reading playlist from YouTube")
		defer ph.finish()
		return yt.Playlist(ctx, link)
	}
	ph := rep.ui.phase("Reading song from YouTube")
	defer ph.finish()
	return yt.Video(ctx, link)
}

func isYouTube(link string) bool {
	_, playlist := youtube.PlaylistID(link)
	_, song := youtube.VideoID(link)
	return playlist || song
}

// songFinder puts a name to a playlist video: the catalog entry for the
// song it plays, with album, cover, year and ISRC. Deezer comes first: it
// allows 50 requests every 5 seconds, and its entries carry the ISRC.
// Apple, which allows about 20 a minute, is asked only for the songs
// Deezer's region-filtered catalog doesn't have. One client each, shared by
// every worker, so their limits hold for the whole run.
type songFinder struct {
	dz      *deezer.Client
	apple   *itunes.Client
	appleRL *rate.Limiter
}

func newSongFinder(cfg config.Config) *songFinder {
	return &songFinder{
		dz:      deezer.New(""),
		apple:   itunes.New("", cfg.Search.Country),
		appleRL: rate.NewLimiter(rate.Every(3*time.Second), 3),
	}
}

// find returns the catalog's version of the song a video plays, or false
// if neither catalog has one that fits. A match titled other than the
// video (pickSong) is kept only until the other catalog has had its say.
func (f *songFinder) find(ctx context.Context, video spotify.Track) (spotify.Track, bool) {
	var fallback *spotify.Track
	query := video.Artists[0] + " " + video.Title
	if found, err := f.dz.Search(ctx, query); err == nil {
		if t, exact, ok := pickSong(video, itunes.Rank(query, found)); ok {
			// Search results lack the featured artists, year and ISRC.
			if full, err := f.dz.Lookup(ctx, strings.TrimPrefix(t.ID, deezer.RefPrefix)); err == nil {
				t = full
			}
			if exact {
				return t, true
			}
			fallback = &t
		}
	} else {
		slog.DebugContext(ctx, "Deezer search failed", "query", query, "err", err)
	}
	// The title alone second: a label's channel ("Fueled By Ramen") posts
	// "twenty one pilots: Stressed Out", and the channel's name in the
	// query only buries the song.
	for _, q := range []string{query, video.Title} {
		if err := f.appleRL.Wait(ctx); err != nil {
			break
		}
		found, err := f.apple.Search(ctx, q)
		if err != nil {
			slog.DebugContext(ctx, "Apple search failed", "query", q, "err", err)
			break
		}
		if t, exact, ok := pickSong(video, itunes.Rank(q, found)); ok {
			if exact {
				return t, true
			}
			if fallback == nil {
				fallback = &t
			}
			break
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return spotify.Track{}, false
}

// songLonger is how much longer than its video a catalog song may be. A
// music video is often longer than the song (an intro, a skit), rarely
// shorter: a song that runs well past the video is another version.
const songLonger = 15 * time.Second

// pickSong takes the best-ranked result whose title and main artist the
// video names. Catalogs rank covers and karaoke first for some songs, and
// these checks, not the rank, are what keep them out. A result titled
// exactly as the video wins over one with more in brackets ("Let Her Go"
// over "Let Her Go (Recorded at Deezer, Sao Paulo)"): an extra bracket is
// usually a session or a special edition, and not every such word is a
// variant word.
func pickSong(video spotify.Track, ranked []itunes.Result) (t spotify.Track, exact, ok bool) {
	named := textnorm.Tokens(video.Title + " " + strings.Join(video.Artists, " "))
	want := textnorm.Norm(textnorm.StripFeat(video.Title))
	var loose *itunes.Result
	for i, r := range ranked {
		if len(r.Artists) == 0 || r.Duration > video.Duration+songLonger {
			continue
		}
		if len(textnorm.Variants(r.Title, video.Title)) > 0 {
			continue // a remix or live take the video didn't ask for
		}
		if !allIn(textnorm.Tokens(textnorm.StripVersion(textnorm.StripFeat(r.Title))), named) ||
			!allIn(textnorm.Tokens(r.Artists[0]), named) {
			continue
		}
		if textnorm.Norm(textnorm.StripFeat(r.Title)) == want {
			return r.Track, true, true
		}
		if loose == nil {
			loose = &ranked[i]
		}
	}
	if loose != nil {
		return loose.Track, false, true
	}
	return spotify.Track{}, false, false
}

func allIn(want, have []string) bool {
	if len(want) == 0 {
		return false
	}
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}
