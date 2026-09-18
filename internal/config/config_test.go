package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	defaults := Default()
	defaults.Output = filepath.Join(home, "Music")

	tests := []struct {
		name    string
		body    string
		env     map[string]string
		flags   map[string]string
		want    func(*Config)
		wantErr error
	}{
		{name: "empty file is all defaults", want: func(*Config) {}},
		{
			name: "file values and tilde expansion",
			body: `output = "~/lib"
format = "flac"
bitrate = "320k"
[youtube]
max_duration_diff = "4s"
extra_args = ["--geo-bypass"]
cookies_file = "~/c.txt"
[spotify]
client_id = "id"
client_secret = "secret"
`,
			want: func(c *Config) {
				c.Output = filepath.Join(home, "lib")
				c.Format = "flac"
				c.Bitrate = "320k"
				c.YouTube.MaxDurationDiff = Duration{4 * time.Second}
				c.YouTube.ExtraArgs = []string{"--geo-bypass"}
				c.YouTube.CookiesFile = filepath.Join(home, "c.txt")
				c.Spotify = Spotify{ClientID: "id", ClientSecret: "secret"}
			},
		},
		{
			name:  "env beats file, flag beats env",
			body:  "jobs = 2\nformat = \"flac\"\n",
			env:   map[string]string{"SPOTIFY_DL_JOBS": "3", "SPOTIFY_DL_FORMAT": "mp3", "SPOTIFY_DL_YOUTUBE_SEARCH_RESULTS": "9", "SPOTIFY_DL_OVERWRITE": "1"},
			flags: map[string]string{"jobs": "5", "youtube.extra_args": "--proxy socks5://x"},
			want: func(c *Config) {
				c.Jobs = 5
				c.Format = "mp3"
				c.YouTube.SearchResults = 9
				c.Overwrite = true
				c.YouTube.ExtraArgs = []string{"--proxy", "socks5://x"}
			},
		},
		{name: "unknown key", body: "fromat = \"mp3\"\n", wantErr: ErrInvalid},
		{name: "half the credentials", body: "[spotify]\nclient_id = \"id\"\n", wantErr: ErrInvalid},
		{name: "bad progress", flags: map[string]string{"progress": "sometimes"}, wantErr: ErrInvalid},
		{name: "unknown format", body: "format = \"wav\"\n", wantErr: ErrInvalid},
		{name: "bad bitrate", flags: map[string]string{"bitrate": "loud"}, wantErr: ErrInvalid},
		{name: "bad bool from env", env: map[string]string{"SPOTIFY_DL_OVERWRITE": "sometimes"}, wantErr: ErrInvalid},
		{name: "bad int from env", env: map[string]string{"SPOTIFY_DL_JOBS": "many"}, wantErr: ErrInvalid},
		{name: "bad duration", flags: map[string]string{"youtube.max_duration_diff": "10"}, wantErr: ErrInvalid},
		{name: "bad template", flags: map[string]string{"output_template": "{artist}/{album}"}, wantErr: ErrInvalid},
		{name: "query without title", flags: map[string]string{"youtube.search_query": "{artists}"}, wantErr: ErrInvalid},
		{name: "both cookie sources", body: "[youtube]\ncookies_file = \"a\"\ncookies_from_browser = \"firefox\"\n", wantErr: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := Load(writeConfig(t, tt.body), tt.flags)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := defaults
			tt.want(&want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := Load(filepath.Join(t.TempDir(), "nope.toml"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.Output = filepath.Join(home, "Music")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSettingNames(t *testing.T) {
	var c Config
	seen := map[string]bool{}
	for _, s := range c.Settings() {
		for kind, name := range map[string]string{"key": s.Key, "flag": s.Flag(), "env": s.Env()} {
			if seen[kind+":"+name] {
				t.Errorf("duplicate %s %q", kind, name)
			}
			seen[kind+":"+name] = true
			seen[name] = true
		}
	}
	for _, want := range []string{"resolve-jobs", "SPOTIFY_DL_RESOLVE_JOBS", "youtube-max-duration-diff", "SPOTIFY_DL_TOOLS_YT_DLP"} {
		if !seen[want] {
			t.Errorf("missing setting name %q", want)
		}
	}
}

// Every Config field must be reachable from Settings, or it could be set in
// the file but not from flags or the environment.
func TestSettingsCoverConfig(t *testing.T) {
	var c Config
	covered := map[any]bool{}
	for _, s := range c.Settings() {
		covered[s.ptr] = true
	}
	var walk func(v reflect.Value, path string)
	walk = func(v reflect.Value, path string) {
		for i := range v.NumField() {
			f := v.Field(i)
			name := path + v.Type().Field(i).Name
			if f.Kind() == reflect.Struct && f.Type() != reflect.TypeFor[Duration]() {
				walk(f, name+".")
				continue
			}
			if !covered[f.Addr().Interface()] {
				t.Errorf("Config.%s has no Setting", name)
			}
		}
	}
	walk(reflect.ValueOf(&c).Elem(), "")
}
