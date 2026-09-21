package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// mpvEngine drives mpv through its JSON IPC socket. That socket is what
// makes an exact position available, which synced lyrics need: guessing
// from a timer drifts, and drifting lyrics are worse than none.
type mpvEngine struct {
	cmd  *exec.Cmd
	conn net.Conn
	sock string

	mu      sync.Mutex
	status  Status
	nextID  int
	pending map[int]chan mpvReply
	closed  bool
}

type mpvReply struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
	ID    int             `json:"request_id"`
	Event string          `json:"event"`
	Name  string          `json:"name"`
	// ObserveID is the id given to observe_property. Events carry it as
	// "id", which is a different field from a command's "request_id".
	ObserveID int `json:"id"`
}

// observed properties, each with the id it reports under.
const (
	propTimePos = 1
	propDur     = 2
	propPause   = 3
)

func newMPV(ctx context.Context, bin string) (*mpvEngine, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "geet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, fmt.Sprintf("mpv-%d.sock", os.Getpid()))
	_ = os.Remove(sock)

	cmd := exec.Command(bin,
		"--no-video", "--idle=yes", "--no-terminal", "--really-quiet",
		// Keep the file open at the end rather than quitting, so the
		// queue, not mpv, decides what happens next.
		"--keep-open=yes",
		"--input-ipc-server="+sock,
	)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	conn, err := dialSocket(ctx, sock, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return nil, fmt.Errorf("mpv did not open its control socket: %w", err)
	}

	e, err := attachMPV(conn, cmd, sock)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// attachMPV wires an engine to an open IPC connection. It is separate from
// starting mpv so the protocol can be tested against a fake socket.
func attachMPV(conn net.Conn, cmd *exec.Cmd, sock string) (*mpvEngine, error) {
	e := &mpvEngine{cmd: cmd, conn: conn, sock: sock, pending: map[int]chan mpvReply{}}
	e.status.Seekable = true
	go e.read()

	for _, p := range []struct {
		id   int
		name string
	}{{propTimePos, "time-pos"}, {propDur, "duration"}, {propPause, "pause"}} {
		if err := e.send("observe_property", p.id, p.name); err != nil {
			_ = e.Close()
			return nil, err
		}
	}
	return e, nil
}

// dialSocket waits for mpv to create the socket, which takes a moment.
func dialSocket(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// read consumes mpv's event stream: property changes update the status,
// command replies go to whoever is waiting for them.
func (e *mpvEngine) read() {
	sc := bufio.NewScanner(e.conn)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var msg mpvReply
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Event != "" {
			e.handleEvent(msg)
			continue
		}
		e.mu.Lock()
		ch, ok := e.pending[msg.ID]
		delete(e.pending, msg.ID)
		e.mu.Unlock()
		if ok {
			ch <- msg
			close(ch)
		}
	}
	// The socket closed: mpv is gone, so nothing is playing any more.
	e.mu.Lock()
	e.status.Playing = false
	e.mu.Unlock()
}

func (e *mpvEngine) handleEvent(msg mpvReply) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch msg.Event {
	case "property-change":
		switch msg.ObserveID {
		case propTimePos:
			var v float64
			if json.Unmarshal(msg.Data, &v) == nil {
				e.status.Position = time.Duration(v * float64(time.Second))
			}
		case propDur:
			var v float64
			if json.Unmarshal(msg.Data, &v) == nil {
				e.status.Duration = time.Duration(v * float64(time.Second))
			}
		case propPause:
			var v bool
			if json.Unmarshal(msg.Data, &v) == nil {
				e.status.Playing = !v
			}
		}
	case "end-file":
		e.status.Ended = true
		e.status.Playing = false
	case "file-loaded":
		e.status.Ended = false
		e.status.Playing = true
	}
}

func (e *mpvEngine) send(args ...any) error {
	_, err := e.request(args...)
	return err
}

// request sends a command and waits for its reply, so a failure (a missing
// file, a bad seek) surfaces as an error instead of silence.
func (e *mpvEngine) request(args ...any) (json.RawMessage, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, os.ErrClosed
	}
	e.nextID++
	id := e.nextID
	ch := make(chan mpvReply, 1)
	e.pending[id] = ch
	e.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"command": args, "request_id": id})
	if err != nil {
		return nil, err
	}
	if _, err := e.conn.Write(append(payload, '\n')); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		if reply.Error != "" && reply.Error != "success" {
			return nil, fmt.Errorf("mpv: %s", reply.Error)
		}
		return reply.Data, nil
	case <-time.After(3 * time.Second):
		e.mu.Lock()
		delete(e.pending, id)
		e.mu.Unlock()
		return nil, fmt.Errorf("mpv did not answer")
	}
}

func (e *mpvEngine) Play(ctx context.Context, path string) error {
	e.mu.Lock()
	e.status = Status{Path: path, Seekable: true}
	e.mu.Unlock()
	if err := e.send("loadfile", path, "replace"); err != nil {
		return err
	}
	return e.send("set_property", "pause", false)
}

func (e *mpvEngine) TogglePause() (bool, error) {
	data, err := e.request("cycle", "pause")
	if err != nil {
		return e.Status().Playing, err
	}
	_ = data
	// The pause property is observed, but the display asks immediately, so
	// read it back rather than waiting for the event.
	raw, err := e.request("get_property", "pause")
	if err != nil {
		return e.Status().Playing, err
	}
	var paused bool
	if err := json.Unmarshal(raw, &paused); err != nil {
		return e.Status().Playing, err
	}
	e.mu.Lock()
	e.status.Playing = !paused
	e.mu.Unlock()
	return !paused, nil
}

func (e *mpvEngine) Seek(delta time.Duration) error {
	return e.send("seek", delta.Seconds(), "relative")
}

func (e *mpvEngine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *mpvEngine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()

	_, _ = e.conn.Write([]byte(`{"command":["quit"]}` + "\n"))
	_ = e.conn.Close()
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		_, _ = e.cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = e.cmd.Process.Kill()
		<-done
	}
	_ = os.Remove(e.sock)
	return nil
}
