package spotify

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// countingTransport counts requests by path. Album requests can be slowed,
// so concurrent workers overlap while one is in flight.
type countingTransport struct {
	mu         sync.Mutex
	paths      map[string]int
	albumDelay time.Duration
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.paths[r.URL.Path]++
	c.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/embed/album/") {
		time.Sleep(c.albumDelay)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (c *countingTransport) count(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for p, k := range c.paths {
		if strings.HasPrefix(p, prefix) {
			n += k
		}
	}
	return n
}

func counting(w *Web) *countingTransport {
	ct := &countingTransport{paths: map[string]int{}}
	w.http.Transport = ct
	return ct
}

// Reading a playlist again reads only the playlist itself: every track comes
// from the cache, identical to what the pages gave the first time.
func TestWebCacheSkipsPageReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spotify.json")
	ctx := context.Background()

	first := newTestWeb(t)
	first.Cache = LoadCache(path, time.Hour)
	ct := counting(first)
	col1, err := first.Resolve(ctx, Ref{KindPlaylist, "pl"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Cache.Save(); err != nil {
		t.Fatal(err)
	}
	if got := ct.count("/track/"); got != 3 { // t2, tx, and "gone", which 404s
		t.Fatalf("first run read %d track pages, want 3", got)
	}

	second := newTestWeb(t)
	second.Cache = LoadCache(path, time.Hour)
	ct = counting(second)
	col2, err := second.Resolve(ctx, Ref{KindPlaylist, "pl"})
	if err != nil {
		t.Fatal(err)
	}
	// Only the track Spotify no longer has is asked for again.
	if got, want := ct.paths, map[string]int{"/embed/playlist/pl": 1, "/track/gone": 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("second run requests %v, want %v", got, want)
	}
	if !reflect.DeepEqual(col1.Tracks, col2.Tracks) {
		t.Errorf("cached tracks differ:\n%+v\n%+v", col1.Tracks, col2.Tracks)
	}
}

func TestCacheExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spotify.json")
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	c := LoadCache(path, time.Hour)
	c.now = func() time.Time { return now }
	c.put(Track{ID: "t1", Title: "One"})
	if _, ok := c.get("t1"); !ok {
		t.Fatal("fresh entry missing")
	}
	now = now.Add(2 * time.Hour)
	if _, ok := c.get("t1"); ok {
		t.Error("expired entry still returned")
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if n := LoadCache(path, time.Hour).Len(); n != 0 {
		t.Errorf("saved cache holds %d entries, want the expired one dropped", n)
	}
}

func TestCacheIgnoresBadFiles(t *testing.T) {
	for name, content := range map[string]string{
		"corrupt":       "{not json",
		"other version": `{"version":99,"tracks":{"t1":{"track":{"ID":"t1"},"fetched":"2026-09-18T00:00:00Z"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "spotify.json")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			c := LoadCache(path, 100*365*24*time.Hour)
			if c.Len() != 0 {
				t.Fatalf("loaded %d entries from a bad file", c.Len())
			}
			c.put(Track{ID: "t2"})
			if err := c.Save(); err != nil {
				t.Fatalf("saving over a bad file: %v", err)
			}
			if _, ok := LoadCache(path, time.Hour).get("t2"); !ok {
				t.Error("entry not saved")
			}
		})
	}
}

// watch and a download can run side by side; neither save may drop what
// the other one read.
func TestCacheSaveKeepsOtherProcessEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spotify.json")
	a := LoadCache(path, time.Hour)
	b := LoadCache(path, time.Hour)
	a.put(Track{ID: "t1"})
	b.put(Track{ID: "t2"})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	c := LoadCache(path, time.Hour)
	for _, id := range []string{"t1", "t2"} {
		if _, ok := c.get(id); !ok {
			t.Errorf("%s lost", id)
		}
	}
}

// Workers reading songs of the same album share one album fetch.
func TestWebFetchesEachAlbumOnce(t *testing.T) {
	w := newTestWeb(t)
	w.Workers = 8
	ct := counting(w)
	ct.albumDelay = 100 * time.Millisecond
	ids := []string{"t1", "t2", "t1", "t2", "t1", "t2", "t1", "t2"}
	tracks, _, err := w.Tracks(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != len(ids) {
		t.Fatalf("got %d tracks, want %d", len(tracks), len(ids))
	}
	if got := ct.count("/embed/album/"); got != 1 {
		t.Errorf("album fetched %d times, want once", got)
	}
}

func TestWebPacesRequests(t *testing.T) {
	if got := NewWeb("").pace.Limit(); got != requestsPerSecond {
		t.Errorf("default pace %v requests/s, want %v", got, requestsPerSecond)
	}

	w := newTestWeb(t)
	w.Workers = 8
	w.pace = rate.NewLimiter(20, 1) // one request per 50ms
	start := time.Now()
	// 4 track pages and 1 album page: 5 requests, at least 4 gaps.
	if _, _, err := w.Tracks(context.Background(), []string{"t1", "t2", "t1", "t2"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 190*time.Millisecond {
		t.Errorf("8 workers sent 5 requests in %s; paced at 20/s they need at least 200ms", elapsed)
	}
}
