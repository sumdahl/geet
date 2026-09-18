package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name    string
		body    string
		want    Config
		wantErr error
	}{
		{
			name: "defaults fill unset keys",
			body: "[spotify]\nclient_id = \"id\"\nclient_secret = \"secret\"\n",
			want: Config{
				Spotify:     Spotify{ClientID: "id", ClientSecret: "secret"},
				Output:      filepath.Join(home, "Music"),
				Format:      "opus",
				Jobs:        4,
				ResolveJobs: 8,
			},
		},
		{
			name: "overrides and tilde expansion",
			body: "output = \"~/lib\"\nformat = \"flac\"\njobs = 2\nresolve_jobs = 3\nbitrate = \"320k\"\n[spotify]\nclient_id = \"id\"\nclient_secret = \"secret\"\n",
			want: Config{
				Spotify:     Spotify{ClientID: "id", ClientSecret: "secret"},
				Output:      filepath.Join(home, "lib"),
				Format:      "flac",
				Bitrate:     "320k",
				Jobs:        2,
				ResolveJobs: 3,
			},
		},
		{
			name:    "missing credentials",
			body:    "format = \"mp3\"\n",
			wantErr: ErrInvalid,
		},
		{
			name:    "unknown format",
			body:    "format = \"wav\"\n[spotify]\nclient_id = \"id\"\nclient_secret = \"secret\"\n",
			wantErr: ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
}
