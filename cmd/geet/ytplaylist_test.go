package main

import (
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/youtube"
)

func TestPickSong(t *testing.T) {
	t.Parallel()
	song := func(title, artist string, secs int) itunes.Result {
		return itunes.Result{Track: spotify.Track{ID: title, Title: title, Artists: []string{artist}, Duration: time.Duration(secs) * time.Second}}
	}
	video := func(title, channel string, secs int) spotify.Track {
		v := youtube.FromVideo(title, channel)
		v.Duration = time.Duration(secs) * time.Second
		return v
	}
	cases := []struct {
		name    string
		video   spotify.Track
		results []itunes.Result
		want    string // ID picked, "" for none
	}{
		{"plain", video("Ed Sheeran - Perfect (Official Music Video)", "Ed Sheeran", 282),
			[]itunes.Result{song("Perfect", "Ed Sheeran", 263)}, "Perfect"},
		{"a long music video still fits its song", video("Maroon 5 - Don't Wanna Know (Official Music Video)", "Maroon 5", 379),
			[]itunes.Result{song("Don't Wanna Know (feat. Kendrick Lamar)", "Maroon 5", 214)}, "Don't Wanna Know (feat. Kendrick Lamar)"},
		{"artist and title swapped", video("Back To The Frontier - JT Catalano (Official Music Video)", "JT Catalano", 237),
			[]itunes.Result{song("Back to the Frontier", "JT Catalano", 230)}, "Back to the Frontier"},
		{"another artist's song of that name", video("Imagine Dragons - Thunder", "ImagineDragons", 205),
			[]itunes.Result{song("Thunder", "Gabry Ponte", 180)}, ""},
		{"a remix the video didn't ask for", video("Dua Lipa - New Rules (Official Music Video)", "Dua Lipa", 225),
			[]itunes.Result{song("New Rules (Initial Talk Remix)", "Dua Lipa", 200), song("New Rules", "Dua Lipa", 209)}, "New Rules"},
		{"a song far longer than the video", video("Khalid - Talk (Official Video)", "Khalid", 194),
			[]itunes.Result{song("Talk", "Khalid", 260)}, ""},
		{"the song over a session take ranked above it", video("Passenger | Let Her Go (Official Video)", "Passenger", 255),
			[]itunes.Result{song("Let Her Go (Recorded at Deezer, Sao Paulo)", "Passenger", 250), song("Let Her Go", "Passenger", 252)}, "Let Her Go"},
		{"an acoustic take the video didn't ask for", video("Ed Sheeran - Perfect (Official Music Video)", "Ed Sheeran", 282),
			[]itunes.Result{song("Perfect (Acoustic Session)", "Ed Sheeran", 260)}, ""},
		{"nothing found", video("Lightly Child", "Roberto Hutchins", 229), nil, ""},
	}
	for _, c := range cases {
		got, _, ok := pickSong(c.video, c.results)
		if ok != (c.want != "") || got.ID != c.want {
			t.Errorf("%s: picked %q (%v), want %q", c.name, got.ID, ok, c.want)
		}
	}
}
