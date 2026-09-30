package config

import "testing"

// Every listed choice must be a value the config accepts, and nothing else:
// a front end builds its pickers from these.
func TestChoicesAreValid(t *testing.T) {
	c := Default()
	for _, s := range c.Settings() {
		for _, choice := range s.Choices {
			cfg := Default()
			for _, cs := range cfg.Settings() {
				if cs.Key == s.Key {
					if err := cs.Set(choice); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("%s = %q: %v", s.Key, choice, err)
			}
		}
	}
}
