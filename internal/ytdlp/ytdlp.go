// Package ytdlp runs the yt-dlp binary with the user's shared settings
// (cookies, extra arguments). Search and download both go through it.
package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

var (
	ErrToolMissing = errors.New("yt-dlp not found")
	// ErrBotCheck is YouTube refusing this IP until it proves it's human
	// ("Sign in to confirm you're not a bot"). Unlike a stray 403 it doesn't
	// pass by retrying: retries only prolong it.
	ErrBotCheck = errors.New("YouTube wants a sign-in to confirm you're not a bot")
)

// BotCheckFix is how to get past ErrBotCheck.
const BotCheckFix = `set youtube.cookies_from_browser to a browser where you're signed in to YouTube (e.g. "chromium" or "firefox"), or wait an hour and lower jobs`

type Runner struct {
	Binary             string
	CookiesFile        string
	CookiesFromBrowser string
	ExtraArgs          []string
}

// Run executes yt-dlp with the shared arguments, then args, and returns its
// stdout.
func (r Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := r.RunLines(ctx, func(line string) {
		out.WriteString(line)
		out.WriteByte('\n')
	}, args...)
	return out.Bytes(), err
}

// RunLines is Run, but hands over each stdout line as it arrives, so
// progress can be shown while yt-dlp is still running. A failure carries
// yt-dlp's last stderr line, which is where it explains itself ("Sign in to
// confirm you're not a bot", "Video unavailable").
func (r Runner) RunLines(ctx context.Context, onLine func(string), args ...string) error {
	full := make([]string, 0, len(args)+len(r.ExtraArgs)+2)
	switch {
	case r.CookiesFile != "":
		full = append(full, "--cookies", r.CookiesFile)
	case r.CookiesFromBrowser != "":
		full = append(full, "--cookies-from-browser", r.CookiesFromBrowser)
	}
	full = append(full, r.ExtraArgs...)
	full = append(full, args...)

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, r.Binary, full...)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: %q (install yt-dlp or set tools.yt_dlp)", ErrToolMissing, r.Binary)
		}
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		onLine(sc.Text())
	}
	// Drain anything the scanner gave up on so yt-dlp never blocks writing.
	io.Copy(io.Discard, stdout)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isBotCheck(stderr.String()) {
			return fmt.Errorf("%w: %s", ErrBotCheck, BotCheckFix)
		}
		// Lead with yt-dlp's own explanation: it is what a one-line display
		// has room for, and "exit status 1" says nothing.
		return fmt.Errorf("yt-dlp: %s (%w)", reason(stderr.String()), err)
	}
	return sc.Err()
}

func isBotCheck(stderr string) bool {
	return strings.Contains(stderr, "confirm you") && strings.Contains(stderr, "not a bot")
}

func reason(stderr string) string {
	line := lastLine(stderr)
	line = strings.TrimPrefix(line, "ERROR: ")
	if line == "" {
		return "failed without an error message"
	}
	return line
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
