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
