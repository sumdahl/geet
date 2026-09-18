package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fake(t *testing.T) (bin, clip string) {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-wl-paste")
	if err != nil {
		t.Fatal(err)
	}
	clip = filepath.Join(t.TempDir(), "clipboard")
	t.Setenv("FAKE_CLIPBOARD", clip)
	t.Setenv("FAKE_WLPASTE_ERROR", "")
	return bin, clip
}

// copyText replaces the fake clipboard atomically, so a read never sees a
// half-written file.
func copyText(t *testing.T, clip, text string) {
	t.Helper()
	tmp := clip + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, clip); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	bin, clip := fake(t)
	ctx := context.Background()

	if got, err := Read(ctx, bin); err != nil || got != "" {
		t.Errorf("nothing copied: got %q, %v; want empty, nil", got, err)
	}

	copyText(t, clip, "https://open.spotify.com/track/4rXLjWdF2ZZpXCVTfWcshS")
	if got, err := Read(ctx, bin); err != nil || got != "https://open.spotify.com/track/4rXLjWdF2ZZpXCVTfWcshS" {
		t.Errorf("got %q, %v", got, err)
	}

	copyText(t, clip, strings.Repeat("x", maxText+100))
	if got, err := Read(ctx, bin); err != nil || len(got) != maxText {
		t.Errorf("huge clipboard: got %d bytes, %v; want %d", len(got), err, maxText)
	}

	t.Setenv("FAKE_WLPASTE_ERROR", `Clipboard content is not available as requested type "text"`)
	if got, err := Read(ctx, bin); err != nil || got != "" {
		t.Errorf("image copied: got %q, %v; want empty, nil", got, err)
	}

	t.Setenv("FAKE_WLPASTE_ERROR", "Failed to connect to a Wayland server")
	if _, err := Read(ctx, bin); err == nil || !strings.Contains(err.Error(), "Wayland server") {
		t.Errorf("no Wayland: got %v, want wl-paste's own message", err)
	}

	if _, err := Read(ctx, "definitely-not-wl-paste"); !errors.Is(err, ErrToolMissing) {
		t.Errorf("missing binary: got %v, want ErrToolMissing", err)
	}
}

func TestWatch(t *testing.T) {
	bin, clip := fake(t)
	copyText(t, clip, "copied before the watcher started")

	ctx, cancel := context.WithCancel(context.Background())
	changes := make(chan string, 10)
	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, bin, 10*time.Millisecond, func(text string) { changes <- text })
	}()

	next := func() string {
		t.Helper()
		select {
		case s := <-changes:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no change reported")
			return ""
		}
	}
	// Each step waits for a poll to see the clipboard; the fake's
	// clipboard file changes between polls.
	settle := func() { time.Sleep(60 * time.Millisecond) }

	settle()
	copyText(t, clip, "first")
	if got := next(); got != "first" {
		t.Errorf("got %q, want first (the baseline must not be reported)", got)
	}
	settle()
	copyText(t, clip, "first") // same text again: no change
	settle()
	copyText(t, clip, "  ") // blank: remembered but not reported
	settle()
	copyText(t, clip, "first") // copied again after something else
	if got := next(); got != "first" {
		t.Errorf("got %q, want first", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Watch returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch didn't return after cancel")
	}
	select {
	case s := <-changes:
		t.Errorf("unexpected extra change %q", s)
	default:
	}
}

func TestWatchFailsFast(t *testing.T) {
	bin, _ := fake(t)
	t.Setenv("FAKE_WLPASTE_ERROR", "Failed to connect to a Wayland server")
	err := Watch(context.Background(), bin, time.Hour, func(string) { t.Error("onChange called") })
	if err == nil {
		t.Fatal("got nil, want the first read's error")
	}
}
