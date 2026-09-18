package textnorm

import "testing"

func TestNorm(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Back In The U.S.S.R.", "back in the u s s r"},
		{"  Help!  ", "help"},
		{"Beyoncé", "beyoncé"},
		{"ROSÉ — new trick", "rosé new trick"},
		{"हताररिँदै, बतासिँदै", "हताररिँदै बतासिँदै"},
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
