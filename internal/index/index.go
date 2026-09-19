// Package index remembers every file geet has saved, by Spotify track
// ID and ISRC, so a song that turns up again (in another playlist, or on
// both an album and a single) is linked or copied instead of downloaded
// twice.
package index

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/sumdahl/geet/internal/itunes"
)

// Entry is one track in one format. Path is its first copy; Also lists
// its other copies, such as the same song hard-linked into another playlist's
// folder. Without them, deleting the first copy lost track of the others and
// the song was downloaded again. Older index files have no "also" and load
// as before; older geet versions ignore it.
type Entry struct {
	Path string   `json:"path"`
	ISRC string   `json:"isrc,omitempty"`
	Also []string `json:"also,omitempty"`
}

// paths is every copy, the first copy first.
func (e Entry) paths() []string {
	return append([]string{e.Path}, e.Also...)
}

// Index is safe for concurrent use.
type Index struct {
	path   string
	saveMu sync.Mutex // one Save at a time, so an older snapshot never lands last

	mu     sync.Mutex
	tracks map[string]Entry    // by key(track ID, extension)
	isrcs  map[string][]string // key(ISRC, extension) -> every tracks key with that recording
}

// key keeps one entry per format, so an opus and an mp3 of the same track
// don't displace each other.
func key(id, ext string) string {
	return id + "." + strings.ToLower(strings.TrimPrefix(ext, "."))
}

// DefaultPath is $XDG_DATA_HOME/geet/index.json.
func DefaultPath() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "geet", "index.json"), nil
}

// Open loads the index at path. fresh reports that no index existed yet, so
// files saved before the index was introduced aren't known (see Scan).
func Open(path string) (idx *Index, fresh bool, err error) {
	idx = &Index{path: path, tracks: map[string]Entry{}, isrcs: map[string][]string{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return idx, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	var doc struct {
		Tracks map[string]Entry `json:"tracks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, false, fmt.Errorf("reading index %s: %w", path, err)
	}
	for k, e := range doc.Tracks {
		idx.put(k, e)
	}
	return idx, false, nil
}

// Lookup returns an existing file for the track in format ext: by track ID,
// or by ISRC (the same recording under another ID: an album and a single, a
// Spotify link and a search result). With several copies, it prefers one on
// the same filesystem as near (where the new file goes), because only that
// can be hard-linked; the rest would be copied. Entries whose file has been
// deleted are forgotten.
func (x *Index) Lookup(id, isrc, ext, near string) (string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	candidates := []string{key(id, ext)}
	if isrc != "" {
		candidates = append(candidates, x.isrcs[key(isrc, ext)]...)
	}
	nearDev, nearOK := deviceOf(near)
	var fallback string
	seen := map[string]bool{}
	for _, k := range candidates {
		if !x.prune(k) {
			continue
		}
		for _, p := range x.tracks[k].paths() {
			if seen[p] {
				continue
			}
			seen[p] = true
			if dev, ok := deviceOf(p); nearOK && ok && dev == nearDev {
				return p, true
			}
			if fallback == "" {
				fallback = p
			}
		}
	}
	return fallback, fallback != ""
}

// prune drops the copies of k whose file is gone, promoting a remaining copy
// to first if the first one went, and forgets k when none is left. It
// reports whether any copy remains. The caller holds x.mu.
func (x *Index) prune(k string) bool {
	e, ok := x.tracks[k]
	if !ok {
		return false
	}
	var alive []string
	for _, p := range e.paths() {
		if _, err := os.Stat(p); err == nil {
			alive = append(alive, p)
		}
	}
	if len(alive) == 0 {
		x.remove(k)
		return false
	}
	e.Path, e.Also = alive[0], alive[1:]
	if len(e.Also) == 0 {
		e.Also = nil
	}
	x.tracks[k] = e
	return true
}

// deviceOf is the filesystem holding path, or its nearest existing parent
// (a new playlist folder doesn't exist yet when its tracks are looked up).
func deviceOf(path string) (uint64, bool) {
	for p := path; ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				return 0, false
			}
			return uint64(st.Dev), true
		}
		if parent := filepath.Dir(p); parent == p {
			return 0, false
		}
	}
}

// Add records a saved file. A song already known keeps its first copy, and
// the new file is recorded as another copy, so the song can still be linked
// from it after the first copy is deleted.
func (x *Index) Add(id, isrc, path string) {
	if id == "" {
		return
	}
	k := key(id, filepath.Ext(path))
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.tracks[k]; !ok || !x.prune(k) {
		x.put(k, Entry{Path: path, ISRC: isrc})
		return
	}
	e := x.tracks[k]
	if !slices.Contains(e.paths(), path) {
		e.Also = append(e.Also, path)
	}
	if e.ISRC == "" {
		e.ISRC = isrc
	}
	x.put(k, e)
}

func (x *Index) put(k string, e Entry) {
	x.tracks[k] = e
	if e.ISRC != "" {
		ik := key(e.ISRC, filepath.Ext(e.Path))
		if !slices.Contains(x.isrcs[ik], k) {
			x.isrcs[ik] = append(x.isrcs[ik], k)
		}
	}
}

func (x *Index) remove(k string) {
	if e, ok := x.tracks[k]; ok && e.ISRC != "" {
		ik := key(e.ISRC, filepath.Ext(e.Path))
		x.isrcs[ik] = slices.DeleteFunc(x.isrcs[ik], func(s string) bool { return s == k })
		if len(x.isrcs[ik]) == 0 {
			delete(x.isrcs, ik)
		}
	}
	delete(x.tracks, k)
}

// Stats counts the known songs and how many of them have no copy left on
// disk, without changing anything.
func (x *Index) Stats() (total, missing int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, e := range x.tracks {
		gone := true
		for _, p := range e.paths() {
			if _, err := os.Stat(p); err == nil {
				gone = false
				break
			}
		}
		if gone {
			missing++
		}
	}
	return len(x.tracks), missing
}

func (x *Index) Len() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return len(x.tracks)
}

// Save writes the index atomically, so an interrupted run never leaves a
// half-written file behind.
func (x *Index) Save() error {
	x.saveMu.Lock()
	defer x.saveMu.Unlock()
	x.mu.Lock()
	b, err := json.MarshalIndent(struct {
		Tracks map[string]Entry `json:"tracks"`
	}{x.tracks}, "", "  ")
	x.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(x.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(x.path), ".index-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), x.path)
}

var audioExts = map[string]bool{".opus": true, ".mp3": true, ".flac": true}

// Scan adds every audio file under root that geet tagged: its comment is
// the track's Spotify URL, or its Apple Music URL for search downloads. Tags
// are read with ffprobe. It's for building the index from a library
// downloaded before the index existed. onProgress, if set, gets files done
// and the total.
func (x *Index) Scan(ctx context.Context, ffprobe, root string, onProgress func(done, total int)) error {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipAll
			}
			return nil // unreadable subtree: skip it, don't fail the run
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != root {
			return filepath.SkipDir // our own .geet-* work dirs, hidden dirs
		}
		if !d.IsDir() && audioExts[strings.ToLower(filepath.Ext(p))] {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}

	var mu sync.Mutex
	done := 0
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for _, f := range files {
		g.Go(func() error {
			if id, isrc, ok := readTags(ctx, ffprobe, f); ok {
				x.Add(id, isrc, f)
			}
			mu.Lock()
			done++
			if onProgress != nil {
				onProgress(done, len(files))
			}
			mu.Unlock()
			return ctx.Err()
		})
	}
	return g.Wait()
}

const trackURLPrefix = "https://open.spotify.com/track/"

func readTags(ctx context.Context, ffprobe, path string) (id, isrc string, ok bool) {
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error",
		"-show_entries", "format_tags:stream_tags", "-of", "default=noprint_wrappers=1", path).Output()
	if err != nil {
		return "", "", false
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20) // a cover can arrive as one very long tag line
	for sc.Scan() {
		k, v, found := strings.Cut(sc.Text(), "=")
		if !found {
			continue
		}
		k = strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(k, "TAG:"), "tag:"))
		switch k {
		case "comment":
			if rest, ok := strings.CutPrefix(v, trackURLPrefix); ok {
				id = rest
			} else if itID, err := itunes.ParseRef(v); err == nil {
				id = itunes.RefPrefix + itID // a search download: Apple Music link
			}
		case "isrc", "tsrc":
			isrc = v
		}
	}
	return id, isrc, id != ""
}

// Place puts src's file at dest without downloading: a hard link (no extra
// disk space; both names are equal, deleting one keeps the other), falling
// back to a copy across filesystems or where links aren't supported.
// copyOnly skips the link attempt. It reports whether it linked.
func Place(src, dest string, copyOnly bool) (linked bool, err error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	if !copyOnly && os.Link(src, dest) == nil {
		return true, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".geet-copy-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return false, os.Rename(tmp.Name(), dest)
}
