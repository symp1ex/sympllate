package language

import "testing"

func TestSimpleDetector(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Привет, как дела?": "ru", "Привіт, як справи?": "uk", "Hello world": "en", "Straße und Größe": "de",
		"¿Cómo está?": "es", "Cześć, jak się masz?": "pl", "こんにちは世界": "ja", "你好世界": "zh", "안녕하세요": "ko", "مرحبا بالعالم": "ar",
	}
	for text, want := range tests {
		if got := (SimpleDetector{}).Detect(text); got != want {
			t.Errorf("Detect(%q) = %q, want %q", text, got, want)
		}
	}
}

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
