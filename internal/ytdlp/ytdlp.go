// Package ytdlp runs the yt-dlp binary with the user's shared settings
// (cookies, extra arguments). Search and download both go through it.
package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrToolMissing = errors.New("yt-dlp not found")

type Runner struct {
	Binary             string
	CookiesFile        string
	CookiesFromBrowser string
	ExtraArgs          []string
}

// Run executes yt-dlp with the shared arguments, then args, and returns its
// stdout. A failure carries yt-dlp's last stderr line, which is where it
// explains itself ("Sign in to confirm you're not a bot", "Video unavailable").
func (r Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	full := make([]string, 0, len(args)+len(r.ExtraArgs)+2)
	switch {
	case r.CookiesFile != "":
		full = append(full, "--cookies", r.CookiesFile)
	case r.CookiesFromBrowser != "":
		full = append(full, "--cookies-from-browser", r.CookiesFromBrowser)
	}
	full = append(full, r.ExtraArgs...)
	full = append(full, args...)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Binary, full...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: %q (install yt-dlp or set tools.yt_dlp)", ErrToolMissing, r.Binary)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp: %w: %s", err, lastLine(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
