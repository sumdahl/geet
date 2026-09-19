package index

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func touch(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLookup(t *testing.T) {
	dir := t.TempDir()
	idx, fresh, err := Open(filepath.Join(dir, "index.json"))
	if err != nil || !fresh {
		t.Fatalf("Open: fresh=%v err=%v", fresh, err)
	}
	opus := touch(t, filepath.Join(dir, "mix-1", "Stan - Eminem.opus"), "audio")
	gone := filepath.Join(dir, "mix-1", "Deleted.opus")
	idx.Add("stan", "USIR10000449", opus)
	idx.Add("deleted", "", gone)

	tests := []struct {
		name, id, isrc, ext string
		want                string
	}{
		{name: "same spotify id", id: "stan", ext: "opus", want: opus},
		{name: "same recording under another id", id: "stan-single", isrc: "USIR10000449", ext: "opus", want: opus},
		{name: "other format is not a duplicate", id: "stan", ext: "mp3"},
		{name: "unknown track", id: "other", isrc: "XX", ext: "opus"},
		{name: "deleted file is forgotten", id: "deleted", ext: "opus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := idx.Lookup(tt.id, tt.isrc, tt.ext, dir)
			if ok != (tt.want != "") || got != tt.want {
				t.Errorf("Lookup = %q, %v; want %q", got, ok, tt.want)
			}
		})
	}
	if idx.Len() != 1 {
		t.Errorf("Len = %d after the deleted entry was dropped, want 1", idx.Len())
	}
}

func TestAddKeepsFirstExistingCopy(t *testing.T) {
	dir := t.TempDir()
	idx, _, _ := Open(filepath.Join(dir, "index.json"))
	first := touch(t, filepath.Join(dir, "a", "s.opus"), "x")
	second := touch(t, filepath.Join(dir, "b", "s.opus"), "x")
	idx.Add("s", "", first)
	idx.Add("s", "", second)
	if got, _ := idx.Lookup("s", "", "opus", ""); got != first {
		t.Errorf("got %q, want the first copy %q", got, first)
	}
	os.Remove(first)
	idx.Add("s", "", second)
	if got, _ := idx.Lookup("s", "", "opus", ""); got != second {
		t.Errorf("got %q, want %q once the first is gone", got, second)
	}
}

func TestSaveAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "index.json")
	idx, _, _ := Open(path)
	f := touch(t, filepath.Join(dir, "x.opus"), "x")
	idx.Add("x", "ISRC1", f)
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	again, fresh, err := Open(path)
	if err != nil || fresh {
		t.Fatalf("reopen: fresh=%v err=%v", fresh, err)
	}
	if got, ok := again.Lookup("other-id", "ISRC1", "opus", ""); !ok || got != f {
		t.Errorf("ISRC lookup after reopen = %q, %v", got, ok)
	}
}

func TestPlace(t *testing.T) {
	dir := t.TempDir()
	src := touch(t, filepath.Join(dir, "a", "song.opus"), "audio bytes")
	for _, copyOnly := range []bool{false, true} {
		dest := filepath.Join(dir, "b", map[bool]string{false: "linked", true: "copied"}[copyOnly], "song.opus")
		linked, err := Place(src, dest, copyOnly)
		if err != nil {
			t.Fatal(err)
		}
		if linked == copyOnly {
			t.Errorf("copyOnly=%v: linked=%v", copyOnly, linked)
		}
		b, _ := os.ReadFile(dest)
		if string(b) != "audio bytes" {
			t.Errorf("dest content %q", b)
		}
		si, _ := os.Stat(src)
		di, _ := os.Stat(dest)
		if os.SameFile(si, di) != linked {
			t.Errorf("SameFile = %v, linked = %v", os.SameFile(si, di), linked)
		}
	}
}

func TestScan(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	root := t.TempDir()
	mk := func(rel, comment, isrcKey, isrc string) string {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		args := []string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=0.2", "-metadata", "comment=" + comment}
		if isrc != "" {
			args = append(args, "-metadata", isrcKey+"="+isrc)
		}
		if filepath.Ext(p) == ".mp3" {
			args = append(args, "-id3v2_version", "3")
		}
		if out, err := exec.Command("ffmpeg", append(args, p)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v: %s", err, out)
		}
		return p
	}
	opus := mk("daily-mix-1/Stan - Eminem.opus", "https://open.spotify.com/track/stanID", "ISRC", "USIR1")
	mp3 := mk("Mask Off - Future.mp3", "https://open.spotify.com/track/maskID", "TSRC", "USSM2")
	searched := mk("Blinding Lights - The Weeknd.opus", "https://music.apple.com/us/album/blinding-lights/1499378108?i=1499378607&uo=4", "", "")
	mk("not-ours.opus", "ripped from a CD", "", "")
	mk(".geet-123/source.opus", "https://open.spotify.com/track/workdir", "", "")

	idx, _, _ := Open(filepath.Join(t.TempDir(), "index.json"))
	var last [2]int
	if err := idx.Scan(context.Background(), "ffprobe", root, func(d, n int) { last = [2]int{d, n} }); err != nil {
		t.Fatal(err)
	}
	if last != [2]int{4, 4} {
		t.Errorf("progress ended at %v, want 4/4 (hidden work dir skipped)", last)
	}
	if idx.Len() != 3 {
		t.Errorf("indexed %d files, want 3", idx.Len())
	}
	if got, _ := idx.Lookup("itunes:1499378607", "", "opus", ""); got != searched {
		t.Errorf("search download = %q", got)
	}
	if got, _ := idx.Lookup("stanID", "", "opus", ""); got != opus {
		t.Errorf("stan = %q", got)
	}
	if got, _ := idx.Lookup("other", "USSM2", "mp3", ""); got != mp3 {
		t.Errorf("mask off by ISRC = %q", got)
	}
}

func TestScanMissingRoot(t *testing.T) {
	idx, _, _ := Open(filepath.Join(t.TempDir(), "index.json"))
	if err := idx.Scan(context.Background(), "ffprobe", "/nonexistent/music", nil); err != nil {
		t.Fatal(err)
	}
}

// Several copies of one recording (e.g. a test copy in /tmp and the real
// one in ~/Music): the one on the destination's filesystem wins, since only
// it can be hard-linked. Here both sit on one filesystem, so the check is
// that a copy registered later under another ID doesn't displace the first,
// and that a deleted copy falls back to the other.
func TestLookupPrefersUsableCopy(t *testing.T) {
	dir := t.TempDir()
	idx, _, _ := Open(filepath.Join(dir, "index.json"))
	music := touch(t, filepath.Join(dir, "Music", "Blinding Lights.opus"), "x")
	other := touch(t, filepath.Join(dir, "elsewhere", "Blinding Lights.opus"), "x")
	idx.Add("itunes:1", "USUG11904206", music)
	idx.Add("itunes:2", "USUG11904206", other) // another edition, same recording

	got, ok := idx.Lookup("spotify-id", "USUG11904206", "opus", filepath.Join(dir, "Music", "new-playlist"))
	if !ok || got != music {
		t.Errorf("got %q, want the first copy %q (same filesystem, registered first)", got, music)
	}
	os.Remove(music)
	if got, _ := idx.Lookup("spotify-id", "USUG11904206", "opus", dir); got != other {
		t.Errorf("after deleting it: got %q, want %q", got, other)
	}
}

func TestDeviceOfMissingPath(t *testing.T) {
	dir := t.TempDir()
	a, ok1 := deviceOf(dir)
	b, ok2 := deviceOf(filepath.Join(dir, "not", "yet", "created"))
	if !ok1 || !ok2 || a != b {
		t.Errorf("deviceOf: %d,%v vs %d,%v", a, ok1, b, ok2)
	}
}

// The same song in two playlists' folders: deleting one copy must leave
// the other findable, so re-running that playlist links it again instead of
// downloading. Deleting both makes it a download again.
func TestIndexRemembersEveryCopy(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "A", "song.opus")
	b := filepath.Join(dir, "B", "song.opus")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, _, err := Open(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	idx.Add("t1", "ISRC1", a)
	idx.Add("t1", "ISRC1", b)
	idx.Add("t1", "ISRC1", b) // recorded once
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}

	// Reopened from disk, both copies are known.
	idx, _, err = Open(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.tracks["t1.opus"]; got.Path != a || !reflect.DeepEqual(got.Also, []string{b}) {
		t.Fatalf("entry %+v, want %s with also %s", got, a, b)
	}

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if got, ok := idx.Lookup("t1", "", ".opus", filepath.Dir(a)); !ok || got != b {
		t.Errorf("first copy deleted: Lookup = %q, %v; want the other copy %s", got, ok, b)
	}
	// By ISRC too (the same recording under another track ID).
	if got, ok := idx.Lookup("other-id", "ISRC1", ".opus", filepath.Dir(a)); !ok || got != b {
		t.Errorf("by ISRC: Lookup = %q, %v; want %s", got, ok, b)
	}
	if total, missing := idx.Stats(); total != 1 || missing != 0 {
		t.Errorf("Stats = %d, %d; want 1 song, 0 missing", total, missing)
	}

	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if got, ok := idx.Lookup("t1", "ISRC1", ".opus", dir); ok {
		t.Errorf("every copy deleted: Lookup = %q, want nothing", got)
	}
	if idx.Len() != 0 {
		t.Errorf("forgotten song still counted: Len = %d", idx.Len())
	}
}

// An index written before copies were recorded loads as before, and one
// written now still reads as the old format (older geet ignores "also").
func TestIndexFormatCompatibility(t *testing.T) {
	dir := t.TempDir()
	song := filepath.Join(dir, "song.opus")
	if err := os.WriteFile(song, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := `{"tracks": {"t1.opus": {"path": "` + song + `", "isrc": "ISRC1"}}}`
	p := filepath.Join(dir, "index.json")
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, fresh, err := Open(p)
	if err != nil || fresh {
		t.Fatalf("Open old format: fresh %v, err %v", fresh, err)
	}
	if got, ok := idx.Lookup("t1", "", ".opus", dir); !ok || got != song {
		t.Errorf("old format: Lookup = %q, %v", got, ok)
	}

	other := filepath.Join(dir, "copy.opus")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx.Add("t1", "ISRC1", other)
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var asOld struct {
		Tracks map[string]struct {
			Path string `json:"path"`
			ISRC string `json:"isrc"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(b, &asOld); err != nil || asOld.Tracks["t1.opus"].Path != song {
		t.Errorf("new file as the old format: %+v, %v", asOld, err)
	}
}
