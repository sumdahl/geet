package ytdlp

import (
	"context"
	"errors"
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
	if !errors.Is(err, ErrBotCheck) || strings.Contains(err.Error(), "wiki/FAQ") || !strings.Contains(err.Error(), "cookies_from_browser") {
		t.Errorf("bot check: %v", err)
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
