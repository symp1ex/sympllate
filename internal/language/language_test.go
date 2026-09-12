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

func TestFallbackSourceLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "cyrillic", text: "Привет", want: "ru"},
		{name: "Ukrainian-specific Cyrillic", text: "Привіт", want: "uk"},
		{name: "unsupported Cyrillic stays supported", text: "ў", want: "ru"},
		{name: "plain Latin", text: "hello", want: "en"},
		{name: "German", text: "groß", want: "de"},
		{name: "Spanish", text: "mañana", want: "es"},
		{name: "Spanish punctuation", text: "¿123?", want: "es"},
		{name: "Polish", text: "cześć", want: "pl"},
		{name: "Portuguese", text: "ação", want: "pt"},
		{name: "Turkish", text: "ışık", want: "tr"},
		{name: "French", text: "Noël", want: "fr"},
		{name: "Italian", text: "così", want: "it"},
		{name: "kana with Han", text: "日本語かな", want: "ja"},
		{name: "Hangul", text: "안녕", want: "ko"},
		{name: "Han", text: "你好", want: "zh"},
		{name: "Arabic", text: "مرحبا", want: "ar"},
		{name: "no language signal", text: "123 ! 😊", want: "en"},
		{name: "ambiguous Latin mark", text: "façade", want: "en"},
		{name: "conflicting Latin signals", text: "groß mañana", want: "en"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := FallbackSourceLanguage(test.text)
			if got != test.want {
				t.Fatalf("FallbackSourceLanguage(%q) = %q, want %q", test.text, got, test.want)
			}
			if got == "auto" || !IsSupported(got) {
				t.Fatalf("FallbackSourceLanguage(%q) returned unsupported source %q", test.text, got)
			}
		})
	}
}
