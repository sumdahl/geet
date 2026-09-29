package youtube

import (
	"os"
	"slices"
	"testing"
	"time"
)

// Titles from a real YouTube Music playlist (testdata/playlist.json).
func TestFromVideo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title, channel string
		song           string
		artists        []string
	}{
		{"Taylor Swift - Blank Space", "Taylor Swift", "Blank Space", []string{"Taylor Swift"}},
		{"Ed Sheeran - Perfect (Official Music Video)", "Ed Sheeran", "Perfect", []string{"Ed Sheeran"}},
		{"Luis Fonsi, Daddy Yankee - Despacito (Audio) ft. Justin Bieber", "Luis Fonsi", "Despacito", []string{"Luis Fonsi", "Daddy Yankee", "Justin Bieber"}},
		{"Maroon 5 - Girls Like You ft. Cardi B (Official Music Video)", "Maroon 5", "Girls Like You", []string{"Maroon 5", "Cardi B"}},
		{"Major Lazer & DJ Snake - Lean On (feat. MØ) [Official 4K Music Video]", "Major Lazer Official", "Lean On", []string{"Major Lazer", "DJ Snake", "MØ"}},
		{"The Weeknd - Starboy ft. Daft Punk (Official Video) ft. Daft Punk", "The Weeknd", "Starboy", []string{"The Weeknd", "Daft Punk"}},
		{"Jason Derulo - Swalla (feat. Nicki Minaj & Ty Dolla $ign) [Official Music Video]", "Jason Derulo", "Swalla", []string{"Jason Derulo", "Nicki Minaj", "Ty Dolla $ign"}},
		{"Jessie J - Bang Bang ft. Ariana Grande, Nicki Minaj", "Jessie J", "Bang Bang", []string{"Jessie J", "Ariana Grande", "Nicki Minaj"}},
		{"benny blanco, Halsey & Khalid – Eastside (official video)", "benny blanco", "Eastside", []string{"benny blanco", "Halsey", "Khalid"}},
		{"Lady Gaga, Bradley Cooper - Shallow (from A Star Is Born) (Official Music Video)", "Lady Gaga", "Shallow", []string{"Lady Gaga", "Bradley Cooper"}},
		{"Selena Gomez - Come & Get It", "Selena Gomez", "Come & Get It", []string{"Selena Gomez"}},
		{"Ariana Grande - thank u, next (Official Video)", "Ariana Grande", "thank u, next", []string{"Ariana Grande"}},
		{"Y2K, bbno$ - Lalala (Official Video)", "Y2K", "Lalala", []string{"Y2K", "bbno$"}},
		{"Avicii - Levels (Skrillex Remix)", "Avicii", "Levels (Skrillex Remix)", []string{"Avicii"}},
		{"KALYANI (with Shreya Ghoshal) OFFICIAL MUSIC VIDEO | ARJN | KDS", "ARJN", "KALYANI", []string{"ARJN", "Shreya Ghoshal"}},
		{"Lightly Child", "Roberto Hutchins", "Lightly Child", []string{"Roberto Hutchins"}},
		{"Passenger | Let Her Go (Official Video)", "Passenger", "Let Her Go", []string{"Passenger"}},
		{"Blinding Lights", "The Weeknd - Topic", "Blinding Lights", []string{"The Weeknd"}},
		{"Money Dance", "KameronArmorVEVO", "Money Dance", []string{"KameronArmor"}},
	}
	for _, c := range cases {
		got := FromVideo(c.title, c.channel)
		if got.Title != c.song || !slices.Equal(got.Artists, c.artists) {
			t.Errorf("FromVideo(%q) = %q by %q, want %q by %q", c.title, got.Title, got.Artists, c.song, c.artists)
		}
	}
}

func TestPlaylistID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		link, id string
		ok       bool
	}{
		{"https://music.youtube.com/playlist?list=PLplXQ2cg9B_qrCVd1J_iId5SvP8Kf_BfS&si=Tp_XtEw8POOl5062", "PLplXQ2cg9B_qrCVd1J_iId5SvP8Kf_BfS", true},
		{"https://www.youtube.com/playlist?list=PL123", "PL123", true},
		{"https://youtube.com/playlist?list=PL123", "PL123", true},
		{"https://www.youtube.com/watch?v=abc&list=PL123", "", false},
		{"https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M", "", false},
		{"https://music.youtube.com/playlist", "", false},
		{"https://evil.example/playlist?list=PL123", "", false},
	}
	for _, c := range cases {
		id, ok := PlaylistID(c.link)
		if ok != c.ok || (ok && id != c.id) {
			t.Errorf("PlaylistID(%q) = %q, %v; want %q, %v", c.link, id, ok, c.id, c.ok)
		}
	}
}

func TestParsePlaylist(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/playlist.json")
	if err != nil {
		t.Fatal(err)
	}
	col, err := parsePlaylist("PLx", raw)
	if err != nil {
		t.Fatal(err)
	}
	if col.Name != "Top Songs of the Decade Playlist (2010-2019)" || len(col.Tracks) != 3 {
		t.Fatalf("got %q with %d tracks, want the playlist's name and 3 tracks (the deleted video skipped)", col.Name, len(col.Tracks))
	}
	first := col.Tracks[0]
	if first.ID != "youtube:e-ORhEE9VVg" || first.SourceURL != "https://music.youtube.com/watch?v=e-ORhEE9VVg" ||
		first.Duration != 273*time.Second || first.Title != "Blank Space" {
		t.Fatalf("first track = %+v", first)
	}
}

func TestVideoID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		link, id string
		ok       bool
	}{
		{"https://music.youtube.com/watch?v=e-ORhEE9VVg&si=abc", "e-ORhEE9VVg", true},
		{"https://www.youtube.com/watch?v=e-ORhEE9VVg&list=PL123&index=2", "e-ORhEE9VVg", true},
		{"https://m.youtube.com/watch?v=e-ORhEE9VVg", "e-ORhEE9VVg", true},
		{"https://youtu.be/e-ORhEE9VVg?si=abc", "e-ORhEE9VVg", true},
		{"https://www.youtube.com/shorts/e-ORhEE9VVg", "e-ORhEE9VVg", true},
		{"https://music.youtube.com/playlist?list=PL123", "", false},
		{"https://www.youtube.com/@TaylorSwift", "", false},
		{"https://notyoutube.com/watch?v=e-ORhEE9VVg", "", false},
		{"https://open.spotify.com/track/3XtbtOMVVBooqbcGz8UErp", "", false},
	}
	for _, c := range cases {
		id, ok := VideoID(c.link)
		if ok != c.ok || (ok && id != c.id) {
			t.Errorf("VideoID(%q) = %q, %v; want %q, %v", c.link, id, ok, c.id, c.ok)
		}
	}
}

func TestParseVideo(t *testing.T) {
	t.Parallel()
	// A YouTube Music song: its own metadata wins over the title.
	col, err := parseVideo([]byte(`{"id":"e-ORhEE9VVg","title":"Blank Space (Taylor's Version)","channel":"Taylor Swift - Topic",
		"duration":231,"track":"Blank Space","artists":["Taylor Swift"],"album":"1989 (Deluxe)","release_year":2014}`))
	if err != nil {
		t.Fatal(err)
	}
	got := col.Tracks[0]
	if col.Ref.ID != "youtube:e-ORhEE9VVg" || got.Title != "Blank Space" || got.Album != "1989 (Deluxe)" || got.Year != 2014 ||
		!slices.Equal(got.Artists, []string{"Taylor Swift"}) || got.Duration != 231*time.Second {
		t.Fatalf("song = %+v", got)
	}
	// Older yt-dlp: one "artist" string.
	col, _ = parseVideo([]byte(`{"id":"x1","title":"t","channel":"c","duration":100,"track":"Starboy","artist":"The Weeknd, Daft Punk"}`))
	if a := col.Tracks[0].Artists; !slices.Equal(a, []string{"The Weeknd", "Daft Punk"}) {
		t.Fatalf("artists = %q", a)
	}
	// A plain video: read off the title.
	col, _ = parseVideo([]byte(`{"id":"x2","title":"Ed Sheeran - Perfect (Official Music Video)","channel":"Ed Sheeran","duration":282}`))
	if got := col.Tracks[0]; got.Title != "Perfect" || got.Artists[0] != "Ed Sheeran" || col.Name != "Perfect" {
		t.Fatalf("video = %+v", got)
	}
	if _, err := parseVideo([]byte(`{}`)); err == nil {
		t.Fatal("no video accepted")
	}
}
