package ytdlp

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Cookie sources yt-dlp can read: browser names, plus the desktop entry of
// each as `xdg-settings get default-web-browser` reports it.
var browserByDesktop = map[string]string{
	"brave-browser":         "brave",
	"brave":                 "brave",
	"chromium":              "chromium",
	"google-chrome":         "chrome",
	"google-chrome-stable":  "chrome",
	"firefox":               "firefox",
	"vivaldi-stable":        "vivaldi",
	"vivaldi":               "vivaldi",
	"microsoft-edge":        "edge",
	"microsoft-edge-stable": "edge",
	"opera":                 "opera",
}

// chromiumFlagsFile is where each Chromium-family browser reads extra
// command-line flags on Arch/Omarchy, including --password-store.
var chromiumFlagsFile = map[string]string{
	"brave":    "brave-flags.conf",
	"chromium": "chromium-flags.conf",
	"chrome":   "chrome-flags.conf",
	"vivaldi":  "vivaldi-stable.conf",
	"edge":     "microsoft-edge-stable-flags.conf",
	"opera":    "opera-flags.conf",
}

// keyringByPasswordStore maps Chromium's --password-store to yt-dlp's
// keyring names.
var keyringByPasswordStore = map[string]string{
	"gnome-libsecret": "gnomekeyring",
	"gnome-keyring":   "gnomekeyring",
	"gnome":           "gnomekeyring",
	"kwallet":         "kwallet",
	"kwallet5":        "kwallet5",
	"kwallet6":        "kwallet6",
	"basic":           "basictext",
}

var ErrNoDefaultBrowser = errors.New("can't tell which browser is the default (xdg-settings)")

// Probes for the parts of the system CookieSource inspects, replaceable in
// tests.
type Probes struct {
	DefaultBrowser func() (string, error) // desktop entry, e.g. "brave-browser.desktop"
	ConfigDir      string                 // $XDG_CONFIG_HOME
	SecretService  func() bool            // a Secret Service (GNOME Keyring) is running
	KWallet        func() bool
}

// SystemProbes inspects the real system.
func SystemProbes() Probes {
	dir, _ := os.UserConfigDir()
	return Probes{
		DefaultBrowser: func() (string, error) {
			out, err := exec.Command("xdg-settings", "get", "default-web-browser").Output()
			return strings.TrimSpace(string(out)), err
		},
		ConfigDir:     dir,
		SecretService: func() bool { return running("gnome-keyring-daemon") },
		KWallet:       func() bool { return running("kwalletd6") || running("kwalletd5") },
	}
}

func running(name string) bool {
	return exec.Command("pgrep", "-x", name).Run() == nil
}

// CookieSource turns the youtube.cookies_from_browser setting into the value
// for yt-dlp's --cookies-from-browser.
//
// "auto" means the default browser. For Chromium-family browsers the keyring
// holding the cookie key is added when the setting doesn't name one: outside
// GNOME and KDE (Hyprland, sway…) yt-dlp guesses "basictext", can decrypt
// none of the cookies, and silently sends YouTube none. A value that already
// has a keyring ("brave+kwallet6") or profile is left alone.
func CookieSource(setting string, p Probes) (string, error) {
	browser := strings.ToLower(strings.TrimSpace(setting))
	if browser == "" {
		return "", nil
	}
	if browser == "auto" {
		desktop, err := p.DefaultBrowser()
		if err != nil || desktop == "" {
			return "", ErrNoDefaultBrowser
		}
		name, ok := browserByDesktop[strings.TrimSuffix(desktop, ".desktop")]
		if !ok {
			return "", errors.New("the default browser (" + desktop + ") isn't one yt-dlp can read cookies from")
		}
		browser = name
	}
	if strings.ContainsAny(browser, "+:") {
		return browser, nil
	}
	if keyring := chromiumKeyring(browser, p); keyring != "" {
		return browser + "+" + keyring, nil
	}
	return browser, nil
}

func chromiumKeyring(browser string, p Probes) string {
	flags, ok := chromiumFlagsFile[browser]
	if !ok {
		return "" // Firefox and Safari don't use a keyring
	}
	if store := passwordStore(filepath.Join(p.ConfigDir, flags)); store != "" {
		return keyringByPasswordStore[store]
	}
	switch {
	case p.SecretService != nil && p.SecretService():
		return "gnomekeyring"
	case p.KWallet != nil && p.KWallet():
		return "kwallet6"
	}
	return ""
}

// passwordStore reads --password-store=<value> from a Chromium flags file.
func passwordStore(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		for _, field := range strings.Fields(sc.Text()) {
			if v, ok := strings.CutPrefix(field, "--password-store="); ok {
				return v
			}
		}
	}
	return ""
}

// CookieTrouble reads yt-dlp's stderr from a run with cookies and describes
// a silent failure: cookies that couldn't be decrypted are simply not sent.
func CookieTrouble(stderr string) string {
	switch {
	case strings.Contains(stderr, "cannot decrypt"):
		return "yt-dlp can't decrypt the browser's cookies (wrong keyring), so none are sent"
	case strings.Contains(stderr, "Extracted 0 cookies"):
		return "the browser has no cookies yt-dlp can use"
	case strings.Contains(stderr, "could not find") && strings.Contains(stderr, "cookies database"):
		return "that browser's profile wasn't found"
	}
	return ""
}
