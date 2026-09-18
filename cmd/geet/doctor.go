package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/doctor"
	"github.com/sumdahl/geet/internal/index"
)

func doctorCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := newCLI("doctor", "doctor [--offline] [--json] [flags]", stderr)
	offline := c.fs.Bool("offline", false, "skip the network checks (Spotify, YouTube, Deezer, iTunes)")
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil || len(positional) > 0 {
		return exitFatal
	}

	env := doctor.Env{Offline: *offline, Now: time.Now()}
	env.Config, env.ConfigPath, env.ConfigErr = c.load()
	if env.ConfigErr != nil {
		// A broken config is a finding, not a reason to stop: check the rest
		// as if the file weren't there.
		env.Config = withoutFile(c.overrides)
	}
	if !*offline && !c.json {
		fmt.Fprintln(stderr, "Checking tools, setup and services…")
	}
	checks := doctor.Run(ctx, env)
	healthy := doctor.Healthy(checks)

	if c.json {
		out := struct {
			Healthy bool           `json:"healthy"`
			Version string         `json:"version"`
			Checks  []doctor.Check `json:"checks"`
		}{healthy, versionString(), checks}
		if err := writeJSON(stdout, out); err != nil {
			return exitFatal
		}
	} else {
		printChecks(stdout, checks, isTerminal(stdout))
	}
	if !healthy {
		return exitPartial
	}
	return exitOK
}

// withoutFile is the config minus the broken file: defaults, GEET_*
// variables and flags, so the rest of the checks still see what the user
// asked for. If those are invalid too, plain defaults.
func withoutFile(flags map[string]string) config.Config {
	if cfg, err := config.Load("", flags); err == nil {
		return cfg
	}
	cfg := config.Default()
	cfg.Output, _ = config.ExpandHome(cfg.Output)
	cfg.IndexPath, _ = index.DefaultPath()
	return cfg
}

func printChecks(w io.Writer, checks []doctor.Check, color bool) {
	paint := func(code, s string) string {
		if !color || os.Getenv("NO_COLOR") != "" {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	marks := map[doctor.Status]string{
		doctor.OK:   paint("32", "✓"),
		doctor.Warn: paint("33", "!"),
		doctor.Fail: paint("31", "✗"),
		doctor.Skip: paint("2", "○"),
	}

	var fails, warns int
	group := ""
	for _, c := range checks {
		if c.Group != group {
			if group != "" {
				fmt.Fprintln(w)
			}
			group = c.Group
			fmt.Fprintln(w, paint("1", group))
		}
		detail := c.Detail
		if c.MS > 0 && c.Status == doctor.OK {
			detail += paint("2", fmt.Sprintf(" · %.1fs", float64(c.MS)/1000))
		}
		fmt.Fprintf(w, "  %s %-10s %s\n", marks[c.Status], c.Name, detail)
		if c.Fix != "" {
			fmt.Fprintf(w, "    %s %s\n", paint("2", "→"), c.Fix)
		}
		switch c.Status {
		case doctor.Fail:
			fails++
		case doctor.Warn:
			warns++
		}
	}

	fmt.Fprintln(w)
	switch {
	case fails == 0 && warns == 0:
		fmt.Fprintln(w, paint("32", "All good."))
	default:
		var parts []string
		if fails > 0 {
			parts = append(parts, paint("31", plural(fails, "problem")+" to fix"))
		}
		if warns > 0 {
			parts = append(parts, paint("33", plural(warns, "warning")))
		}
		fmt.Fprintln(w, strings.Join(parts, ", ")+".")
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
