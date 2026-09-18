package spotify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// cacheVersion changes whenever the cached Track layout does; a file with
// another version is ignored and rebuilt.
const cacheVersion = 1

// Cache keeps tracks read from Spotify's public pages on disk, so reading a
// playlist again (songs were added, or a run was cut short) doesn't read the
// pages of songs already read. That is most of a big playlist's page reads,
// and page reads are what Spotify rate-limits. Entries expire after the TTL,
// so corrections on Spotify's side come through eventually.
//
// A Cache is safe for concurrent use. It's only a cache: an unreadable file
// starts it empty, and losing it costs page reads, nothing else.
type Cache struct {
	path string
	ttl  time.Duration
	now  func() time.Time

	mu     sync.Mutex
	tracks map[string]cachedTrack
	dirty  bool
}

type cachedTrack struct {
	Track   Track     `json:"track"`
	Fetched time.Time `json:"fetched"`
}

type cacheFile struct {
	Version int                    `json:"version"`
	Tracks  map[string]cachedTrack `json:"tracks"`
}

// LoadCache opens the cache at path, keeping entries for ttl.
func LoadCache(path string, ttl time.Duration) *Cache {
	c := &Cache{path: path, ttl: ttl, now: time.Now, tracks: map[string]cachedTrack{}}
	if f, err := readCacheFile(path); err == nil {
		c.tracks = f.Tracks
	}
	return c
}

func readCacheFile(path string) (cacheFile, error) {
	var f cacheFile
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return cacheFile{}, err
	}
	if f.Version != cacheVersion || f.Tracks == nil {
		return cacheFile{}, errors.New("cache: other version")
	}
	return f, nil
}

func (c *Cache) get(id string) (Track, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.tracks[id]
	if !ok || c.now().Sub(e.Fetched) > c.ttl {
		return Track{}, false
	}
	return e.Track, true
}

func (c *Cache) put(t Track) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tracks[t.ID] = cachedTrack{Track: t, Fetched: c.now()}
	c.dirty = true
}

// Len is how many unexpired tracks the cache holds.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.tracks {
		if c.now().Sub(e.Fetched) <= c.ttl {
			n++
		}
	}
	return n
}

// Save writes new entries to disk, dropping expired ones. Another geet
// process (watch next to a download) may have saved meanwhile, so the file
// is read again and the newer entry of each track kept.
func (c *Cache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return nil
	}
	// An unreadable file, or one of another version, is overwritten.
	if f, err := readCacheFile(c.path); err == nil {
		for id, e := range f.Tracks {
			if mine, ok := c.tracks[id]; !ok || e.Fetched.After(mine.Fetched) {
				c.tracks[id] = e
			}
		}
	}
	now := c.now()
	for id, e := range c.tracks {
		if now.Sub(e.Fetched) > c.ttl {
			delete(c.tracks, id)
		}
	}
	b, err := json.Marshal(cacheFile{Version: cacheVersion, Tracks: c.tracks})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".spotify-cache-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), c.path); err != nil {
		return err
	}
	c.dirty = false
	return nil
}
