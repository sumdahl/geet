package notify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	tests := []struct {
		name string
		m    Message
		want []string
	}{
		{
			name: "summary only",
			m:    Message{Summary: "Saved"},
			want: []string{"--print-id", "--app-name=geet", "--", "Saved"},
		},
		{
			name: "everything",
			m:    Message{Summary: "Saved", Body: "Rock & <Roll>'s", Icon: "/c/cover.jpg", Urgency: Critical, Replaces: 42},
			want: []string{"--print-id", "--app-name=geet", "--urgency=critical", "--icon=/c/cover.jpg", "--replace-id=42", "--", "Saved", "Rock &amp; &lt;Roll&gt;'s"},
		},
		{
			name: "summary starting with a dash",
			m:    Message{Summary: "-ish - song"},
			want: []string{"--print-id", "--app-name=geet", "--", "-ish - song"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := args("geet", tt.m); !slices.Equal(got, tt.want) {
				t.Errorf("args =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestSend(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "notify-send")
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\necho 17\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	id, err := Notifier{Bin: bin, App: "geet"}.Send(context.Background(), Message{Summary: "Saved", Body: "a song"})
	if err != nil || id != 17 {
		t.Fatalf("Send = %d, %v; want 17, nil", id, err)
	}
	got, _ := os.ReadFile(log)
	if want := "--print-id\n--app-name=geet\n--\nSaved\na song\n"; string(got) != want {
		t.Errorf("notify-send got\n%s\nwant\n%s", got, want)
	}

	if _, err := (Notifier{Bin: "definitely-not-notify-send"}).Send(context.Background(), Message{Summary: "x"}); !errors.Is(err, ErrToolMissing) {
		t.Errorf("missing binary: got %v, want ErrToolMissing", err)
	}

	failing := filepath.Join(dir, "failing")
	os.WriteFile(failing, []byte("#!/bin/sh\necho 'Cannot connect to the notification server' >&2\nexit 1\n"), 0o755)
	if _, err := (Notifier{Bin: failing}).Send(context.Background(), Message{Summary: "x"}); err == nil || !strings.Contains(err.Error(), "notification server") {
		t.Errorf("failing notify-send: got %v, want its message", err)
	}
}
