package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sumdahl/geet/internal/notify"
	"github.com/sumdahl/geet/internal/spotify"
)

const (
	trackA   = "https://open.spotify.com/track/4rXLjWdF2ZZpXCVTfWcshS"
	trackB   = "https://open.spotify.com/track/0VjIjW4GlUZAMYd2vXMi3b"
	album    = "https://open.spotify.com/album/1DFixLWuPkv3KT3TnV35m3"
	playlist = "https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M"
)

func TestClipboardJobs(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []watchJob
	}{
		{"one song", trackA + "?si=abc123\n", []watchJob{{source: trackA + "?si=abc123", link: trackA + "?si=abc123"}}},
		{"album", album, []watchJob{{source: album, link: album}}},
		{"uri", "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M", []watchJob{{source: "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M", link: "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"}}},
		{"songs copied together", trackA + "\n" + trackB + "\n" + trackA, []watchJob{{source: trackA, links: []string{trackA, trackB}}}},
		{"link in prose", "listen to this (" + trackA + "), it's great.", []watchJob{{source: trackA, link: trackA}}},
		{
			"playlist, songs and an album",
			playlist + " " + trackA + " " + album + " " + trackB,
			[]watchJob{{source: playlist, link: playlist}, {source: trackA, links: []string{trackA, trackB}}, {source: album, link: album}},
		},
		{"apple music song", "itunes:1499378607", []watchJob{{source: "itunes:1499378607", link: "itunes:1499378607"}}},
		{"deezer song", "https://www.deezer.com/track/10202476", []watchJob{{source: "https://www.deezer.com/track/10202476", link: "https://www.deezer.com/track/10202476"}}},
		{"podcast episode", "https://open.spotify.com/episode/4rXLjWdF2ZZpXCVTfWcshS", nil},
		{"not a link", "just some copied text", nil},
		{"other site", "https://www.youtube.com/watch?v=l21wGxlWwPw", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clipboardJobs(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("clipboardJobs(%q) =\n%+v\nwant\n%+v", tt.text, got, tt.want)
			}
		})
	}
}

func TestFinishMessage(t *testing.T) {
	song := spotify.Collection{Ref: spotify.Ref{Kind: spotify.KindTrack}, Tracks: []spotify.Track{{Title: "fukumean", Artists: []string{"Gunna"}}}}
	albumCol := spotify.Collection{Ref: spotify.Ref{Kind: spotify.KindAlbum}, Name: "a Gift & a Curse", Tracks: make([]spotify.Track, 3)}
	j := watchJob{source: trackA}

	tests := []struct {
		name    string
		col     spotify.Collection
		res     outcome
		err     error
		summary string
		body    string
		urgency notify.Urgency
	}{
		{"song saved", song, outcome{saved: 1}, nil, "Saved", "Gunna - fukumean", notify.Normal},
		{"song already there", song, outcome{existing: 1}, nil, "Already in your library", "Gunna - fukumean", notify.Low},
		{"album saved", albumCol, outcome{saved: 2, existing: 1}, nil, "Saved", "a Gift & a Curse · 2 saved, 1 already there", notify.Normal},
		{"album partly failed", albumCol, outcome{saved: 2, failed: 1}, nil, "Downloaded with failures", "a Gift & a Curse · 2 saved, 1 failed", notify.Normal},
		{"album all failed", albumCol, outcome{failed: 3}, nil, "Download failed", "a Gift & a Curse · 3 failed", notify.Critical},
		{"link unreadable", spotify.Collection{}, outcome{}, errors.New("spotify resource not found"), "Download failed", trackA + "\nspotify resource not found", notify.Critical},
		{"stopped", albumCol, outcome{saved: 1}, errInterrupted, "Download stopped", "a Gift & a Curse · 1 saved", notify.Normal},
		{"empty playlist", spotify.Collection{Ref: spotify.Ref{Kind: spotify.KindPlaylist}, Name: "empty"}, outcome{}, nil, "Nothing to download", "empty has no songs", notify.Normal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := finishMessage(j, tt.col, tt.res, tt.err)
			if m.Summary != tt.summary || m.Body != tt.body || m.Urgency != tt.urgency {
				t.Errorf("got %q / %q / %s, want %q / %q / %s", m.Summary, m.Body, m.Urgency, tt.summary, tt.body, tt.urgency)
			}
		})
	}
}

func readEvents(t *testing.T, b *bytes.Buffer) []event {
	t.Helper()
	var evs []event
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		evs = append(evs, e)
	}
	return evs
}

func TestCopiedQueuesInOrder(t *testing.T) {
	var out, errOut bytes.Buffer
	rep := &reporter{ui: &plainUI{w: &errOut}, stderr: &errOut, warned: map[string]bool{}, json: newJSONEncoder(&out)}
	w := &watcher{rep: rep, jobs: make(chan watchJob, queueSize)}

	w.copied(album)
	w.copied(trackA + "\n" + trackB)
	w.copied("no link here")

	if got := len(w.jobs); got != 2 {
		t.Fatalf("queued %d jobs, want 2", got)
	}
	if j := <-w.jobs; j.id != 1 || j.link != album {
		t.Errorf("first job %+v, want id 1 for the album", j)
	}
	if j := <-w.jobs; j.id != 2 || len(j.links) != 2 {
		t.Errorf("second job %+v, want id 2 with both songs", j)
	}
	want := []event{{Stage: "queued", Job: 1, Source: album}, {Stage: "queued", Job: 2, Source: trackA}}
	if got := readEvents(t, &out); !reflect.DeepEqual(got, want) {
		t.Errorf("events =\n%+v\nwant\n%+v", got, want)
	}
}

func TestCopiedFullQueue(t *testing.T) {
	var out, errOut bytes.Buffer
	rep := &reporter{ui: &plainUI{w: &errOut}, stderr: &errOut, warned: map[string]bool{}, json: newJSONEncoder(&out)}
	w := &watcher{rep: rep, jobs: make(chan watchJob, 1)}

	w.copied(album)
	w.copied(playlist)

	evs := readEvents(t, &out)
	if len(evs) != 3 {
		t.Fatalf("got %d events, want queued, queued, finished: %+v", len(evs), evs)
	}
	// Every queued job gets exactly one "finished", even one dropped at once.
	if last := evs[2]; last.Stage != "finished" || last.Job != 2 || last.Error == "" {
		t.Errorf("dropped job's event %+v, want finished with an error for job 2", last)
	}
	if !strings.Contains(errOut.String(), "ignored") {
		t.Errorf("stderr %q doesn't say the link was ignored", errOut.String())
	}
}

// Events of the link being downloaded carry its job and source, so a
// consumer can tell links apart; queued events set their own.
func TestReporterStampsJob(t *testing.T) {
	var out bytes.Buffer
	rep := &reporter{ui: &plainUI{w: &bytes.Buffer{}}, warned: map[string]bool{}, json: newJSONEncoder(&out)}
	rep.job, rep.source = 3, album
	rep.reading("spotify", 1, 10)
	rep.mu.Lock()
	rep.write(event{Stage: "queued", Job: 4, Source: trackA})
	rep.mu.Unlock()

	evs := readEvents(t, &out)
	if evs[0].Job != 3 || evs[0].Source != album {
		t.Errorf("reading event %+v, want job 3 from %s", evs[0], album)
	}
	if evs[1].Job != 4 || evs[1].Source != trackA {
		t.Errorf("queued event %+v, want its own job 4", evs[1])
	}
}
