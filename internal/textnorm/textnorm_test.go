package textnorm

import (
	"reflect"
	"testing"
)

func TestNorm(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Back In The U.S.S.R.", "back in the u s s r"},
		{"  Help!  ", "help"},
		{"Beyoncé", "beyonce"},
		{"JAŸ-Z", "jay z"},
		{"ROSÉ — new trick", "rose new trick"},
		{"Sigur Rós, Mötley Crüe", "sigur ros motley crue"},
		{"हताररिँदै, बतासिँदै", "हताररिँदै बतासिँदै"},
		{"सज्जन राज वैद्य", "सज्जन राज वैद्य"},
		{"Ni**as In Paris", "ni as in paris"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Norm(tt.in); got != tt.want {
			t.Errorf("Norm(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Back In The U.S.S.R. - Remastered 2009", "back in the u s s r"},
		{"Hataarindai, Bataasindai (feat. Shyam Nepali)", "hataarindai bataasindai"},
		{"Song [Live]", "song"},
		{"(Intro)", "intro"},
		{"Plain", "plain"},
	}
	for _, tt := range tests {
		if got := Base(tt.in); got != tt.want {
			t.Errorf("Base(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWords(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Ni**as In Paris", []string{"ni**as", "in", "paris"}},
		{"N****s in Paris", []string{"n****s", "in", "paris"}},
		{"F*** It", []string{"f***", "it"}},
		{"5 * 5 = *25*", []string{"5", "5", "25*"}},
		{"JAŸ-Z", []string{"jay", "z"}},
	}
	for _, tt := range tests {
		if got := Words(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Words(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWordMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"paris", "paris", true},
		{"ni**as", "niggas", true},
		{"niggas", "ni**as", true},
		{"ni**as", "n****s", true}, // two censorings of the same word
		{"n****s", "niggas", true},
		{"f***", "fuck", true},
		{"f***", "fun", true},
		{"ni**as", "nights", false},
		{"ni**as", "nias", true},
		{"paris", "pari", false},
		{"*", "anything", false},
	}
	for _, tt := range tests {
		if got := WordMatch(tt.a, tt.b); got != tt.want {
			t.Errorf("WordMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
