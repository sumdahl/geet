package ytdlp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCookieSource(t *testing.T) {
	omarchy := t.TempDir() // Omarchy ships Brave with --password-store=gnome-libsecret
	os.WriteFile(filepath.Join(omarchy, "brave-flags.conf"), []byte("--load-extension=/x\n--password-store=gnome-libsecret\n"), 0o644)
	kde := t.TempDir()
	os.WriteFile(filepath.Join(kde, "chromium-flags.conf"), []byte("--password-store=kwallet6"), 0o644)
	empty := t.TempDir()

	probes := func(dir, desktop string, secretService, kwallet bool) Probes {
		return Probes{
			DefaultBrowser: func() (string, error) {
				if desktop == "" {
					return "", errors.New("xdg-settings: not found")
				}
				return desktop, nil
			},
			ConfigDir:     dir,
			SecretService: func() bool { return secretService },
			KWallet:       func() bool { return kwallet },
		}
	}

	tests := []struct {
		name, setting string
		p             Probes
		want          string
		wantErr       bool
	}{
		{"off", "", probes(omarchy, "brave-browser.desktop", true, false), "", false},
		{"auto: Brave on Omarchy", "auto", probes(omarchy, "brave-browser.desktop", true, false), "brave+gnomekeyring", false},
		{"plain brave gets its keyring too", "brave", probes(omarchy, "", true, false), "brave+gnomekeyring", false},
		{"flags file wins over running services", "chromium", probes(kde, "", true, false), "chromium+kwallet6", false},
		{"no flags: GNOME Keyring running", "chrome", probes(empty, "", true, false), "chrome+gnomekeyring", false},
		{"no flags: KWallet running", "vivaldi", probes(empty, "", false, true), "vivaldi+kwallet6", false},
		{"no keyring at all", "chromium", probes(empty, "", false, false), "chromium", false},
		{"firefox needs no keyring", "auto", probes(omarchy, "firefox.desktop", true, false), "firefox", false},
		{"explicit keyring kept", "brave+kwallet5", probes(omarchy, "", true, false), "brave+kwallet5", false},
		{"explicit profile kept", "chromium:Profile 1", probes(omarchy, "", true, false), "chromium:profile 1", false},
		{"unknown default browser", "auto", probes(omarchy, "ladybird.desktop", true, false), "", true},
		{"no default browser", "auto", probes(omarchy, "", true, false), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CookieSource(tt.setting, tt.p)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("CookieSource(%q) = %q, %v; want %q (err=%v)", tt.setting, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestCookieTrouble(t *testing.T) {
	for stderr, wantTrouble := range map[string]bool{
		"WARNING: cannot decrypt v11 cookies: no key found\n":          true,
		"Extracted 0 cookies from brave (1233 could not be decrypted)": true,
		"Extracted 1225 cookies from brave":                            false,
		"":                                                             false,
	} {
		if got := CookieTrouble(stderr) != ""; got != wantTrouble {
			t.Errorf("CookieTrouble(%q) = %v", stderr, got)
		}
	}
}
