package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/sumdahl/geet/internal/itunes"
)

var errNoTerminal = errors.New("no terminal to pick in: pass --pick 1 (or 1,3) to choose, or --json to list results")

// pickResults lets the user choose search results and returns their
// indexes, in the order picked; none means nothing was chosen. mode is the
// search.picker setting: auto uses fzf when it is installed.
func pickResults(ctx context.Context, mode, query string, results []itunes.Result, in *os.File, out io.Writer) ([]int, error) {
	if !term.IsTerminal(int(in.Fd())) {
		return nil, errNoTerminal
	}
	labels := make([]string, len(results))
	for i, r := range results {
		labels[i] = resultLabel(r)
	}
	if mode != "list" {
		if bin, err := exec.LookPath("fzf"); err == nil {
			return pickFzf(ctx, bin, query, labels)
		} else if mode == "fzf" {
			return nil, fmt.Errorf("search.picker is fzf but fzf isn't installed: %w", err)
		}
	}
	return pickList(in, out, labels)
}

// pickFzf shows labels in fzf, which draws on the terminal itself and
// prints the chosen lines. Each line carries its index in a hidden first
// field, so labels that happen to be identical stay distinguishable.
func pickFzf(ctx context.Context, bin, query string, labels []string) ([]int, error) {
	var input strings.Builder
	for i, l := range labels {
		fmt.Fprintf(&input, "%d\t%s\n", i, l)
	}
	cmd := exec.CommandContext(ctx, bin,
		"--multi", "--delimiter", "\t", "--with-nth", "2..",
		"--layout", "reverse", "--height", "50%", "--border", "rounded",
		"--prompt", "Search › ",
		"--header", fmt.Sprintf("Results for %q · Tab: pick several · Enter: choose · Esc: cancel", query),
	)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		// fzf exits 130 when cancelled (Esc, Ctrl+C) and 1 when the typed
		// filter matches nothing: either way, nothing was picked.
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 130 || exit.ExitCode() == 1) {
			return nil, nil
		}
		return nil, fmt.Errorf("fzf: %w", err)
	}
	var picked []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		idx, _, _ := strings.Cut(line, "\t")
		if i, err := strconv.Atoi(idx); err == nil && i >= 0 && i < len(labels) {
			picked = append(picked, i)
		}
	}
	return picked, nil
}

// pickList is the fallback without fzf: a numbered list and a prompt.
func pickList(in io.Reader, out io.Writer, labels []string) ([]int, error) {
	width := len(strconv.Itoa(len(labels)))
	for i, l := range labels {
		fmt.Fprintf(out, "%*d. %s\n", width, i+1, l)
	}
	sc := bufio.NewScanner(in)
	for {
		fmt.Fprintf(out, "\nPick [1-%d, several like 1 3 or 2-4, Enter = 1, q = quit]: ", len(labels))
		if !sc.Scan() {
			fmt.Fprintln(out)
			return nil, sc.Err()
		}
		picked, quit, err := parseChoice(sc.Text(), len(labels))
		if quit {
			return nil, nil
		}
		if err == nil {
			return picked, nil
		}
		fmt.Fprintln(out, err)
	}
}

// parseChoice reads a pick like "", "1", "1 3", "1,3" or "2-4" against n
// results and returns zero-based indexes without repeats. An empty answer
// means the first result; "q" quits.
func parseChoice(s string, n int) (picked []int, quit bool, err error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "":
		return []int{0}, false, nil
	case "q", "quit", "exit":
		return nil, true, nil
	}
	seen := map[int]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' }) {
		lo, hi := f, f
		if a, b, ok := strings.Cut(f, "-"); ok {
			lo, hi = a, b
		}
		from, err1 := strconv.Atoi(lo)
		to, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || from < 1 || to > n || from > to {
			return nil, false, fmt.Errorf("%q isn't a number from 1 to %d", f, n)
		}
		for i := from; i <= to; i++ {
			if !seen[i] {
				seen[i] = true
				picked = append(picked, i-1)
			}
		}
	}
	if len(picked) == 0 {
		return nil, false, fmt.Errorf("pick a number from 1 to %d", n)
	}
	return picked, false, nil
}

// confirmDownload lists the picked songs and asks before downloading them.
// Enter or y means yes; n, q or end of input means no.
func confirmDownload(in io.Reader, out io.Writer, labels []string) bool {
	fmt.Fprintln(out, "Selected:")
	for _, l := range labels {
		fmt.Fprintf(out, "  • %s\n", l)
	}
	noun := "song"
	if len(labels) != 1 {
		noun = "songs"
	}
	sc := bufio.NewScanner(in)
	for {
		fmt.Fprintf(out, "Download %d %s? [Y/n] ", len(labels), noun)
		if !sc.Scan() {
			fmt.Fprintln(out)
			return false
		}
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "", "y", "yes":
			return true
		case "n", "no", "q", "quit":
			return false
		}
	}
}

// resultLabel is one line of the picker: "Title — Artists · Album (Year) m:ss".
func resultLabel(r itunes.Result) string {
	var b strings.Builder
	b.WriteString(r.Title)
	b.WriteString(" — ")
	b.WriteString(strings.Join(r.Artists, ", "))
	if r.Album != "" && r.Album != r.Title && r.Album != r.Title+" - Single" {
		b.WriteString(" · ")
		b.WriteString(r.Album)
	}
	if r.Year > 0 {
		fmt.Fprintf(&b, " (%d)", r.Year)
	}
	fmt.Fprintf(&b, " %d:%02d", int(r.Duration.Minutes()), int(r.Duration.Seconds())%60)
	switch {
	case r.Explicit:
		b.WriteString(" [E]")
	case r.Clean:
		b.WriteString(" (clean)")
	}
	return b.String()
}
