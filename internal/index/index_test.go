package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
			got, ok := idx.Lookup(tt.id, tt.isrc, tt.ext)
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
	if got, _ := idx.Lookup("s", "", "opus"); got != first {
		t.Errorf("got %q, want the first copy %q", got, first)
	}
	os.Remove(first)
	idx.Add("s", "", second)
	if got, _ := idx.Lookup("s", "", "opus"); got != second {
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
	if got, ok := again.Lookup("other-id", "ISRC1", "opus"); !ok || got != f {
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
	mk("not-ours.opus", "ripped from a CD", "", "")
	mk(".geet-123/source.opus", "https://open.spotify.com/track/workdir", "", "")

	idx, _, _ := Open(filepath.Join(t.TempDir(), "index.json"))
	var last [2]int
	if err := idx.Scan(context.Background(), "ffprobe", root, func(d, n int) { last = [2]int{d, n} }); err != nil {
		t.Fatal(err)
	}
	if last != [2]int{3, 3} {
		t.Errorf("progress ended at %v, want 3/3 (hidden work dir skipped)", last)
	}
	if idx.Len() != 2 {
		t.Errorf("indexed %d files, want 2", idx.Len())
	}
	if got, _ := idx.Lookup("stanID", "", "opus"); got != opus {
		t.Errorf("stan = %q", got)
	}
	if got, _ := idx.Lookup("other", "USSM2", "mp3"); got != mp3 {
		t.Errorf("mask off by ISRC = %q", got)
	}
}

func TestScanMissingRoot(t *testing.T) {
	idx, _, _ := Open(filepath.Join(t.TempDir(), "index.json"))
	if err := idx.Scan(context.Background(), "ffprobe", "/nonexistent/music", nil); err != nil {
		t.Fatal(err)
	}
}
