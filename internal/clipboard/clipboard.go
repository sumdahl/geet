// Package clipboard reads the Wayland clipboard through wl-paste
// (wl-clipboard) and reports when its text changes.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

var ErrToolMissing = errors.New("wl-paste not found")

// maxText caps what one read keeps. Links are short, and a copied list of
// a few hundred song links fits easily; a huge clipboard (a whole document)
// shouldn't be held in memory every poll.
const maxText = 256 << 10

// readTimeout bounds one wl-paste run: it waits on the app that owns the
// clipboard, and a hung app would otherwise stall the watcher for good.
const readTimeout = 3 * time.Second

// Read returns the clipboard's text, or "" when nothing is copied or the
// copy isn't text (an image, say).
func Read(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	var out capped
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "--no-newline", "--type", "text")
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("%w: %q (install wl-clipboard or set tools.wl_paste)", ErrToolMissing, bin)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if empty(msg) {
			return "", nil
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("wl-paste: %w", ctx.Err())
		}
		if msg == "" {
			return "", fmt.Errorf("wl-paste: %w", err)
		}
		return "", fmt.Errorf("wl-paste: %s", msg)
	}
	return out.String(), nil
}

// empty tells wl-paste's "there is no text" from real failures. These are
// its own messages (wl-clipboard src/wl-paste.c; the second is its newer
// wording, the third its older one), all with exit status 1.
func empty(stderr string) bool {
	for _, m := range []string{"Nothing is copied", "Clipboard content is not available as", "No suitable type of content copied"} {
		if strings.Contains(stderr, m) {
			return true
		}
	}
	return false
}

// Watch reads the clipboard every interval and calls onChange with the new
// text whenever it changes to something non-empty. What is on the clipboard
// when Watch starts is only the baseline, so a link copied long before the
// daemon started isn't downloaded.
//
// Only the first read can fail Watch (wl-paste missing, no Wayland session);
// later failures are logged and retried. Watch returns nil when ctx ends.
func Watch(ctx context.Context, bin string, interval time.Duration, onChange func(text string)) error {
	last, err := Read(ctx, bin)
	if err != nil {
		return err
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	var lastErr string
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		text, err := Read(ctx, bin)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Once per distinct error: it usually repeats every poll.
			if err.Error() != lastErr {
				slog.WarnContext(ctx, "reading the clipboard", "err", err)
				lastErr = err.Error()
			}
			continue
		}
		lastErr = ""
		if text == last {
			continue
		}
		last = text
		if strings.TrimSpace(text) != "" {
			onChange(text)
		}
	}
}

// capped keeps the first maxText bytes written and drops the rest, while
// still accepting them so wl-paste doesn't block on a full pipe. The buffer
// is a field, not embedded: an embedded bytes.Buffer's ReadFrom would let
// io.Copy bypass Write and the cap.
type capped struct{ buf bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxText - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }
