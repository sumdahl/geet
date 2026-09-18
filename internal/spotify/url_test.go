package spotify

import (
	"errors"
	"testing"
)

func TestParseURL(t *testing.T) {
	const id = "4uLU6hMCjMI75M1A2tKUQC"
	tests := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "https://open.spotify.com/track/" + id, want: Ref{KindTrack, id}},
		{in: "https://open.spotify.com/album/" + id + "?si=abc123", want: Ref{KindAlbum, id}},
		{in: "https://open.spotify.com/playlist/" + id + "/", want: Ref{KindPlaylist, id}},
		{in: "  https://open.spotify.com/intl-de/track/" + id + "\n", want: Ref{KindTrack, id}},
		{in: "http://open.spotify.com/track/" + id, want: Ref{KindTrack, id}},
		{in: "spotify:track:" + id, want: Ref{KindTrack, id}},
		{in: "spotify:playlist:" + id, want: Ref{KindPlaylist, id}},

		{in: "https://open.spotify.com/artist/" + id, wantErr: true},
		{in: "https://open.spotify.com/episode/" + id, wantErr: true},
		{in: "https://example.com/track/" + id, wantErr: true},
		{in: "https://open.spotify.com/track/short", wantErr: true},
		{in: "https://open.spotify.com/track/" + id + "/extra", wantErr: true},
		{in: "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKU-C", wantErr: true},
		{in: "spotify:track", wantErr: true},
		{in: "spotify:user:foo:playlist:" + id, wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseURL(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidURL) {
					t.Fatalf("err = %v, want ErrInvalidURL", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func FuzzParseURL(f *testing.F) {
	for _, s := range []string{"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC", "spotify:track:x", "https://open.spotify.com/intl-ne/playlist/37i9dQZF1E38GaNXgXwvL4?si=a", "%zz", "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ref, err := ParseURL(s)
		if err == nil && (len(ref.ID) != 22 || (ref.Kind != KindTrack && ref.Kind != KindAlbum && ref.Kind != KindPlaylist)) {
			t.Fatalf("ParseURL(%q) accepted %+v", s, ref)
		}
	})
}
