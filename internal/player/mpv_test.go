package player

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// fakeMPV answers like mpv's IPC socket: every command gets a success
// reply, and the test can push events. It lets the protocol be tested
// without mpv, without audio, and without timing luck.
type fakeMPV struct {
	t    *testing.T
	conn net.Conn
	cmds chan []any
}

func startFakeMPV(t *testing.T) (*mpvEngine, *fakeMPV) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "mpv.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	client, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted

	f := &fakeMPV{t: t, conn: server, cmds: make(chan []any, 32)}
	go f.serve()

	e, err := attachMPV(client, nil, sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e, f
}

// serve replies to each command. get_property pause answers with whatever
// the last "cycle pause" left, so TogglePause can be tested end to end.
func (f *fakeMPV) serve() {
	paused := false
	sc := bufio.NewScanner(f.conn)
	for sc.Scan() {
		var msg struct {
			Command   []any `json:"command"`
			RequestID int   `json:"request_id"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || len(msg.Command) == 0 {
			continue
		}
		name, _ := msg.Command[0].(string)
		var data any
		switch name {
		case "cycle":
			paused = !paused
		case "get_property":
			data = paused
		case "quit":
			return
		}
		select {
		case f.cmds <- msg.Command:
		default:
		}
		payload, _ := json.Marshal(map[string]any{"error": "success", "request_id": msg.RequestID, "data": data})
		_, _ = f.conn.Write(append(payload, '\n'))
	}
}

func (f *fakeMPV) event(v map[string]any) {
	payload, _ := json.Marshal(v)
	_, _ = f.conn.Write(append(payload, '\n'))
}

// nextCommand waits for a command whose name matches.
func (f *fakeMPV) nextCommand(name string) []any {
	f.t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case cmd := <-f.cmds:
			if got, _ := cmd[0].(string); got == name {
				return cmd
			}
		case <-deadline:
			f.t.Fatalf("mpv never got a %q command", name)
		}
	}
}

func TestMPVObservesProperties(t *testing.T) {
	e, f := startFakeMPV(t)
	for range 3 {
		f.nextCommand("observe_property")
	}

	if err := e.Play(context.Background(), "/music/song.opus"); err != nil {
		t.Fatal(err)
	}
	load := f.nextCommand("loadfile")
	if len(load) < 2 || load[1] != "/music/song.opus" {
		t.Fatalf("loadfile got %v", load)
	}

	f.event(map[string]any{"event": "file-loaded"})
	f.event(map[string]any{"event": "property-change", "id": propDur, "name": "duration", "data": 312.5})
	f.event(map[string]any{"event": "property-change", "id": propTimePos, "name": "time-pos", "data": 42.25})

	waitFor(t, func() bool {
		s := e.Status()
		return s.Playing && s.Duration == 312500*time.Millisecond && s.Position == 42250*time.Millisecond
	}, "position and duration to arrive")

	if s := e.Status(); !s.Seekable {
		t.Error("mpv must report itself as seekable")
	}
}

func TestMPVTogglePause(t *testing.T) {
	e, f := startFakeMPV(t)
	for range 3 {
		f.nextCommand("observe_property")
	}
	playing, err := e.TogglePause()
	if err != nil {
		t.Fatal(err)
	}
	if playing {
		t.Error("first toggle should pause")
	}
	if got := e.Status().Playing; got {
		t.Error("status still says playing after pausing")
	}
	if playing, err = e.TogglePause(); err != nil || !playing {
		t.Errorf("second toggle should resume: playing=%v err=%v", playing, err)
	}
}

func TestMPVEndOfFile(t *testing.T) {
	e, f := startFakeMPV(t)
	for range 3 {
		f.nextCommand("observe_property")
	}
	f.event(map[string]any{"event": "file-loaded"})
	waitFor(t, func() bool { return e.Status().Playing }, "playback to start")

	f.event(map[string]any{"event": "end-file"})
	waitFor(t, func() bool {
		s := e.Status()
		return s.Ended && !s.Playing
	}, "the track to be reported as finished")
}

func TestMPVSeekSendsRelative(t *testing.T) {
	e, f := startFakeMPV(t)
	for range 3 {
		f.nextCommand("observe_property")
	}
	if err := e.Seek(-5 * time.Second); err != nil {
		t.Fatal(err)
	}
	cmd := f.nextCommand("seek")
	if len(cmd) != 3 || cmd[1].(float64) != -5 || cmd[2] != "relative" {
		t.Fatalf("seek command was %v", cmd)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
