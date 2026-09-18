package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadText(t *testing.T, text string) (Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(p, nil)
}

func TestTemplateLoadsAsDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want, err := loadText(t, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadText(t, Template())
	if err != nil {
		t.Fatalf("template doesn't load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("template loads to\n%+v\nwant the defaults\n%+v", got, want)
	}
}

// Every setting is listed once, under its table, and uncommenting its line
// gives valid TOML holding the default: the rendered value is right.
func TestTemplateListsEverySetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want, err := loadText(t, "")
	if err != nil {
		t.Fatal(err)
	}
	tpl := Template()
	def := Default()
	if strings.Contains(tpl, "\n[") {
		t.Error("template has a [table] header; keys must be written in full")
	}
	for _, s := range def.Settings() {
		line := "# " + s.Key + " = "
		if n := strings.Count(tpl, "\n"+line); n != 1 {
			t.Errorf("%s: listed %d times", s.Key, n)
			continue
		}
		i := strings.Index(tpl, "\n"+line)
		uncommented := tpl[:i+1] + strings.TrimPrefix(tpl[i+1:], "# ")
		got, err := loadText(t, uncommented)
		if err != nil {
			t.Errorf("%s uncommented: %v", s.Key, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s uncommented doesn't hold the default", s.Key)
		}
	}
}

func TestTemplateEditTakesEffect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	edited := strings.Replace(Template(), "# jobs = 4", "jobs = 16", 1)
	edited = strings.Replace(edited, `# format = "opus"`, `format = "flac"`, 1)
	edited = strings.Replace(edited, "# spotify.cache_days = 30", "spotify.cache_days = 0", 1)
	got, err := loadText(t, edited)
	if err != nil {
		t.Fatal(err)
	}
	if got.Jobs != 16 || got.Format != "flac" || got.Spotify.CacheDays != 0 {
		t.Errorf("edits not applied: jobs %d, format %s, cache_days %d", got.Jobs, got.Format, got.Spotify.CacheDays)
	}
}

// A setting added at the end of the file, below every section, still
// counts: this is what a user does, and it was rejected as "tools.jobs"
// when the template had [table] headers.
func TestTemplateAppendedSetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := loadText(t, Template()+"jobs = 16\nyoutube.search_results = 8\n")
	if err != nil {
		t.Fatal(err)
	}
	if got.Jobs != 16 || got.YouTube.SearchResults != 8 {
		t.Errorf("appended settings: jobs %d, search_results %d", got.Jobs, got.YouTube.SearchResults)
	}
}

func TestMisplacedSettingError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := loadText(t, "[tools]\nyt_dlp = \"yt-dlp\"\njobs = 16\n")
	if err == nil || !strings.Contains(err.Error(), `"jobs" is a setting`) || !strings.Contains(err.Error(), "above the first [section]") {
		t.Errorf("err = %v, want a hint that jobs is misplaced under [tools]", err)
	}
	_, err = loadText(t, "nonsense = 1\n")
	if err == nil || strings.Contains(err.Error(), "is a setting") {
		t.Errorf("err = %v, want a plain unknown-key error", err)
	}
}
