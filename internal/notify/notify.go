// Package notify shows desktop notifications through notify-send (libnotify).
package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var ErrToolMissing = errors.New("notify-send not found")

type Urgency string

const (
	Low      Urgency = "low"
	Normal   Urgency = "normal"
	Critical Urgency = "critical"
)

type Message struct {
	Summary string
	Body    string
	Icon    string // an image file (the cover) or an icon name
	Urgency Urgency
	// Replaces is the id of an earlier notification to update in place,
	// such as "Downloading…" becoming "Saved".
	Replaces uint32
}

type Notifier struct {
	Bin string
	App string
}

// sendTimeout bounds one notify-send run: it waits for the notification
// daemon, and a hung daemon mustn't stall the download queue.
const sendTimeout = 5 * time.Second

// Send shows m and returns its id, for Replaces. The id is 0 when this
// notify-send can't report it (before libnotify 0.7.9).
func (n Notifier) Send(ctx context.Context, m Message) (uint32, error) {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, n.Bin, args(n.App, m)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return 0, fmt.Errorf("%w: %q (install libnotify or set tools.notify_send)", ErrToolMissing, n.Bin)
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return 0, fmt.Errorf("notify-send: %s", msg)
		}
		return 0, fmt.Errorf("notify-send: %w", err)
	}
	id, _ := strconv.ParseUint(strings.TrimSpace(stdout.String()), 10, 32)
	return uint32(id), nil
}

func args(app string, m Message) []string {
	a := []string{"--print-id"}
	if app != "" {
		a = append(a, "--app-name="+app)
	}
	if m.Urgency != "" {
		a = append(a, "--urgency="+string(m.Urgency))
	}
	if m.Icon != "" {
		a = append(a, "--icon="+m.Icon)
	}
	if m.Replaces != 0 {
		a = append(a, "--replace-id="+strconv.FormatUint(uint64(m.Replaces), 10))
	}
	// "--" keeps a title that starts with "-" from being read as an option.
	a = append(a, "--", m.Summary)
	if m.Body != "" {
		a = append(a, escapeMarkup.Replace(m.Body))
	}
	return a
}

// escapeMarkup escapes what servers with body markup (mako, the Omarchy
// shell) would otherwise read as markup, so "Rock & <Roll>" shows as typed
// and a title can't smuggle in an <img>. Quotes stay as they are: html's
// &#39; shows literally on servers that don't parse entities.
var escapeMarkup = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
