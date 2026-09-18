package config

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// Template is a config file listing every setting at its default, each
// commented out under its description, so a user uncomments a line and
// changes it. It's generated from Settings, so it can't miss one. The file
// as written loads to the defaults.
//
// Keys are written in full ("spotify.cache_days"), with no [table]
// headers: under a header, a line added at the end of the file ("jobs =
// 16") would silently belong to the last table and be rejected as
// "tools.jobs".
func Template() string {
	def := Default()
	var b strings.Builder
	b.WriteString(`# geet configuration.
#
# Every setting is listed at its default, commented out. To change one,
# remove the "# " before its line and edit the value. Settings left
# commented keep their defaults, even as new versions change them.
#
# The same settings work as command-line flags (--jobs 8) and as GEET_*
# environment variables (GEET_JOBS=8), which override this file.
# "geet config settings" lists them all; "geet config" shows the values
# in effect.
`)
	section := ""
	for _, s := range def.Settings() {
		sec := "General"
		if i := strings.IndexByte(s.Key, '.'); i >= 0 {
			sec = strings.ToUpper(s.Key[:1]) + s.Key[1:i]
			if sec == "Youtube" {
				sec = "YouTube"
			}
		}
		if sec != section {
			section = sec
			fmt.Fprintf(&b, "\n# ---- %s %s\n", sec, strings.Repeat("-", 68-len(sec)))
		}
		b.WriteByte('\n')
		for _, line := range wrap(s.Usage, 74) {
			b.WriteString("# " + line + "\n")
		}
		fmt.Fprintf(&b, "# %s = %s\n", s.Key, tomlValue(s))
	}
	return b.String()
}

// tomlValue renders s's value as TOML, the way the file would spell it.
func tomlValue(s Setting) string {
	var v any
	switch p := s.ptr.(type) {
	case *Duration:
		v = p.String()
	case *string:
		v = *p
	case *bool:
		v = *p
	case *int:
		v = *p
	case *[]string:
		v = *p
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"v": v}); err != nil {
		return s.String()
	}
	out := strings.TrimSpace(strings.TrimPrefix(buf.String(), "v = "))
	if out == "" { // an empty list encodes to nothing
		return "[]"
	}
	return out
}

func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
