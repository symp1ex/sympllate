package language

import "testing"

func TestChooseDirection(t *testing.T) {
	t.Parallel()
	tests := []struct{ detected, source, target string }{{"ru", "ru", "en"}, {"en", "en", "ru"}, {"de", "de", "ru"}, {"", "auto", "ru"}}
	for _, tt := range tests {
		got := ChooseDirection(tt.detected, "ru", "en", "ru")
		if got.Source != tt.source || got.Target != tt.target {
			t.Errorf("ChooseDirection(%q) = %+v", tt.detected, got)
		}
	}
	custom := ChooseDirection("fr", "fr", "de", "en")
	if custom.Target != "de" {
		t.Fatalf("custom pair = %+v", custom)
	}
}

func TestNonCollidingTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		source, target string
		want           string
	}{
		{name: "different languages", source: "ru", target: "en", want: "en"},
		{name: "first collides", source: "ru", target: "ru", want: "en"},
		{name: "second collides", source: "en", target: "en", want: "ru"},
		{name: "language outside pair collides", source: "fr", target: "fr", want: "en"},
		{name: "auto remains unchanged", source: "auto", target: "ru", want: "ru"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NonCollidingTarget(tt.source, tt.target, "ru", "en"); got != tt.want {
				t.Fatalf("NonCollidingTarget(%q, %q) = %q, want %q", tt.source, tt.target, got, tt.want)
			}
		})
	}
}
