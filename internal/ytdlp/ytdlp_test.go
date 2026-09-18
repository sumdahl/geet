package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fake(t *testing.T, stderr string) Runner {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yt-dlp")
	script := "#!/bin/sh\ncat >&2 <<'EOF'\n" + stderr + "\nEOF\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Runner{Binary: bin}
}

func TestRunErrors(t *testing.T) {
	botCheck := "[youtube] abc: Downloading webpage\nERROR: [youtube] abc: Sign in to confirm you’re not a bot. Use --cookies-from-browser or --cookies for the authentication. See  https://github.com/yt-dlp/yt-dlp/wiki/FAQ"
	_, err := fake(t, botCheck).Run(context.Background())
	if !errors.Is(err, ErrBotCheck) || strings.Contains(err.Error(), "wiki/FAQ") {
		t.Errorf("bot check: %v", err)
	}
	if advice := BotCheckAdvice("", err); !strings.Contains(advice, `cookies_from_browser = "auto"`) {
		t.Errorf("advice without cookies: %s", advice)
	}
	if advice := BotCheckAdvice("brave+gnomekeyring", err); !strings.Contains(advice, "signed in to YouTube") {
		t.Errorf("advice with working cookies: %s", advice)
	}

	// Cookies configured, but yt-dlp couldn't decrypt them: it only warns.
	_, err = fake(t, "WARNING: cannot decrypt v11 cookies: no key found\n"+botCheck).Run(context.Background())
	if !errors.Is(err, ErrBotCheck) || !strings.Contains(err.Error(), "decrypt") {
		t.Errorf("bot check with undecryptable cookies: %v", err)
	}
	wrapped := fmt.Errorf("download failed: %w", err)
	if advice := BotCheckAdvice("brave", wrapped); !strings.Contains(advice, "aren't being sent") || !strings.Contains(advice, "geet doctor") {
		t.Errorf("advice with broken cookies: %s", advice)
	}

	_, err = fake(t, "ERROR: [youtube] abc: Video unavailable").Run(context.Background())
	if errors.Is(err, ErrBotCheck) || !strings.HasPrefix(err.Error(), "yt-dlp: [youtube] abc: Video unavailable") {
		t.Errorf("other error: %v", err)
	}

	_, err = Runner{Binary: "definitely-not-yt-dlp"}.Run(context.Background())
	if !errors.Is(err, ErrToolMissing) {
		t.Errorf("missing: %v", err)
	}
}

// The stderr of two real runs on an age-restricted video: without a sign-in,
// and signed in to an account that isn't age-verified.
func TestRunAgeRestricted(t *testing.T) {
	tests := []struct {
		name       string
		stderr     string
		restricted bool
		advice     string
	}{
		{
			name:       "not signed in",
			stderr:     "ERROR: [youtube] -yFj3FvoOWY: Sign in to confirm your age. This video may be inappropriate for some users. Use --cookies-from-browser or --cookies for the authentication.",
			restricted: true,
			advice:     `cookies_from_browser = "auto"`,
		},
		{
			name: "signed in, account not age-verified",
			stderr: "[youtube] -yFj3FvoOWY: This video is age-restricted and YouTube is requiring account age-verification; some formats may be missing\n" +
				"ERROR: [youtube] -yFj3FvoOWY: Requested format is not available. Use --list-formats for a list of available formats",
			restricted: true,
			advice:     "verify your age",
		},
		{
			name:   "a missing format alone is something else",
			stderr: "ERROR: [youtube] abc: Requested format is not available. Use --list-formats for a list of available formats",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fake(t, tt.stderr).Run(context.Background())
			if got := errors.Is(err, ErrAgeRestricted); got != tt.restricted {
				t.Fatalf("age-restricted = %v (%v), want %v", got, err, tt.restricted)
			}
			if !tt.restricted {
				return
			}
			if errors.Is(err, ErrBotCheck) {
				t.Error("also reported as a bot check")
			}
			if advice := AgeRestrictedAdvice(fmt.Errorf("download failed: %w", err)); !strings.Contains(advice, tt.advice) {
				t.Errorf("advice %q lacks %q", advice, tt.advice)
			}
		})
	}
}
