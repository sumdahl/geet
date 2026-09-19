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
	ErrBotCheck = errors.New("YouTube wants you to confirm you're not a bot")
	// ErrAgeRestricted is a video YouTube plays only to a signed-in,
	// age-verified account. Retrying doesn't help either.
	ErrAgeRestricted = errors.New("YouTube age-restricts this video")
	// ErrUnplayable is an upload YouTube won't serve audio for: no format
	// ("Requested format is not available", which is also what a signed-in
	// account that isn't age-verified gets for an age-restricted video, with
	// no warning saying so), removed, or private. Retrying doesn't help;
	// another upload of the song may.
	ErrUnplayable = errors.New("YouTube serves no audio for this upload")
	// ErrSignInRequired is an upload YouTube plays only to a signed-in
	// account, without saying why ("Please sign in"). Unlike ErrBotCheck it
	// is this upload only: others still play. Retrying doesn't help; another
	// upload or a sign-in does.
	ErrSignInRequired = errors.New("YouTube plays this upload only to a signed-in account")
)

// SignInAdvice is what to tell the user after ErrSignInRequired.
const SignInAdvice = `YouTube plays some uploads only to a signed-in account, and for a song with no other upload geet can use, that's the only way to get it. Set youtube.cookies_from_browser = "auto" to use your default browser's YouTube sign-in, then run the same command again.`

// ageRestrictedError is ErrAgeRestricted, noting whether a YouTube account
// was signed in, which decides the fix.
type ageRestrictedError struct{ signedIn bool }

func (e *ageRestrictedError) Error() string {
	if e.signedIn {
		return ErrAgeRestricted.Error() + " (the signed-in account isn't age-verified)"
	}
	return ErrAgeRestricted.Error() + " (sign-in needed)"
}

func (e *ageRestrictedError) Is(target error) bool { return target == ErrAgeRestricted }

// AgeRestrictedAdvice is what to tell the user after ErrAgeRestricted.
func AgeRestrictedAdvice(err error) string {
	var e *ageRestrictedError
	if errors.As(err, &e) && e.signedIn {
		return "YouTube age-restricts some songs and plays them only to an age-verified account. The YouTube account in your browser isn't verified, so YouTube offers only a low-quality video: verify your age in your Google account, then run the same command again."
	}
	return "YouTube age-restricts some songs and plays them only to a signed-in, age-verified account. Set youtube.cookies_from_browser = \"auto\" to use your default browser's YouTube sign-in, then run the same command again."
}

// BotCheckFix is how to get past ErrBotCheck without cookies configured.
const BotCheckFix = `wait an hour and lower jobs, or set youtube.cookies_from_browser = "auto" to use your default browser's YouTube sign-in`

// botCheckError is ErrBotCheck plus why the browser cookies, if any, weren't
// sent: yt-dlp only warns about that, and it changes the fix.
type botCheckError struct{ cookieTrouble string }

func (e *botCheckError) Error() string {
	if e.cookieTrouble != "" {
		return ErrBotCheck.Error() + " (" + e.cookieTrouble + ")"
	}
	return ErrBotCheck.Error()
}

func (e *botCheckError) Is(target error) bool { return target == ErrBotCheck }

// BotCheckAdvice is what to tell the user after ErrBotCheck, given the
// resolved cookie source ("" when cookies are off).
func BotCheckAdvice(cookieSource string, err error) string {
	msg := "YouTube is blocking downloads from this IP (\"confirm you're not a bot\"). "
	var be *botCheckError
	switch {
	case cookieSource == "":
		return msg + "Fix: " + BotCheckFix + " (in geet's config file: \"geet config path\" shows where)."
	case errors.As(err, &be) && be.cookieTrouble != "":
		return msg + "Your cookies (" + cookieSource + ") aren't being sent: " + be.cookieTrouble + `. Run "geet doctor" to check them.`
	}
	return msg + "Cookies from " + cookieSource + " didn't help: make sure you're signed in to YouTube in that browser, or wait an hour and lower jobs."
}

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
	_, _ = io.Copy(io.Discard, stdout)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isBotCheck(stderr.String()) {
			// Short on purpose: it's shown per track, and the fix, which
			// depends on the cookie setup, is shown once by the caller.
			return &botCheckError{cookieTrouble: CookieTrouble(stderr.String())}
		}
		if restricted, signedIn := ageRestriction(stderr.String()); restricted {
			return &ageRestrictedError{signedIn: signedIn}
		}
		if unplayable(stderr.String()) {
			return fmt.Errorf("%w (%s)", ErrUnplayable, reason(stderr.String()))
		}
		if strings.Contains(stderr.String(), "Please sign in") {
			return ErrSignInRequired
		}
		// Lead with yt-dlp's own explanation: it is what a one-line display
		// has room for, and "exit status 1" says nothing.
		return fmt.Errorf("yt-dlp: %s (%w)", reason(stderr.String()), err)
	}
	return sc.Err()
}

// ageRestriction recognizes YouTube's age gate: without a sign-in yt-dlp
// stops at "Sign in to confirm your age"; with one whose account isn't
// age-verified it warns about "account age-verification" and then finds no
// audio format, since only a low-quality video is offered.
func ageRestriction(stderr string) (restricted, signedIn bool) {
	switch {
	case strings.Contains(stderr, "confirm your age"):
		return true, false
	case strings.Contains(stderr, "age-verification") && strings.Contains(stderr, "Requested format is not available"):
		return true, true
	}
	return false, false
}

func unplayable(stderr string) bool {
	for _, m := range []string{"Requested format is not available", "Video unavailable", "Private video", "This video is not available"} {
		if strings.Contains(stderr, m) {
			return true
		}
	}
	return false
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
