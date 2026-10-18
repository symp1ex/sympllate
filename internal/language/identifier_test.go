package language

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type classifierFunc func(string) Detection

func (f classifierFunc) Detect(text string) Detection { return f(text) }

func TestLanguageIdentifierDeterministicScripts(t *testing.T) {
	t.Parallel()
	classifierCalls := 0
	identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection {
		classifierCalls++
		return Detection{Language: "de", Reliable: true}
	}))

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "kana with han", text: "こんにちは世界", want: "ja"},
		{name: "hangul", text: "안녕하세요", want: "ko"},
		{name: "arabic", text: "مرحبا بالعالم", want: "ar"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			detection := identifier.Detect(test.text)
			if detection.Language != test.want || !detection.Reliable || detection.Reason != DetectionReasonScript || detection.Confidence != 1 {
				t.Fatalf("Detect(%q) = %+v", test.text, detection)
			}
		})
	}
	if classifierCalls != 0 {
		t.Fatalf("classifier calls = %d, want 0", classifierCalls)
	}
}

func TestLanguageIdentifierUsesClassifierForAmbiguousScripts(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"hello", "привет", "你好", "hello مرحبا"} {
		calls := 0
		identifier := NewLanguageIdentifier(classifierFunc(func(got string) Detection {
			calls++
			if got != text {
				t.Fatalf("classifier text = %q, want %q", got, text)
			}
			return Detection{Language: "de", Confidence: 0.9, Reliable: true}
		}))
		detection := identifier.Detect(text)
		if calls != 1 || detection.Language != "de" || !detection.Reliable || detection.Reason != DetectionReasonClassifier {
			t.Fatalf("Detect(%q) = %+v, calls = %d", text, detection, calls)
		}
	}
}

func TestLanguageIdentifierEmptyAndWhitespaceDoNotCallClassifier(t *testing.T) {
	t.Parallel()
	calls := 0
	identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection {
		calls++
		return Detection{Language: "en", Reliable: true}
	}))
	for _, text := range []string{"", " \t\r\n "} {
		if detection := identifier.Detect(text); detection.Reliable || detection.Language != "" || detection.Reason != DetectionReasonEmpty {
			t.Fatalf("Detect(%q) = %+v", text, detection)
		}
	}
	if calls != 0 {
		t.Fatalf("classifier calls = %d, want 0", calls)
	}
}

func TestLanguageIdentifierResolveSourcePolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		text      string
		detection Detection
		source    string
		want      string
		calls     int
	}{
		{name: "reliable auto", text: "text", detection: Detection{Language: "de", Reliable: true}, source: "auto", want: "de", calls: 1},
		{name: "unreliable usable auto", text: "This is long enough natural language text for classification.", detection: Detection{Language: "de", Reliable: false}, source: "auto", want: "de", calls: 1},
		{name: "unreliable short auto", text: "test", detection: Detection{Language: "de", Reliable: false}, source: "auto", want: "auto", calls: 1},
		{name: "missing candidate", text: "This is long enough natural language text for classification.", detection: Detection{}, source: "auto", want: "auto", calls: 1},
		{name: "unsupported candidate", text: "This is long enough natural language text for classification.", detection: Detection{Language: "nl", Reliable: true}, source: "auto", want: "auto", calls: 1},
		{name: "explicit bypass", text: "text", detection: Detection{Language: "de", Reliable: true}, source: "fr", want: "fr", calls: 0},
		{name: "empty", text: "", detection: Detection{Language: "uk"}, source: "auto", want: "auto", calls: 0},
		{name: "whitespace", text: " \t\r\n ", detection: Detection{Language: "uk"}, source: "auto", want: "auto", calls: 0},
		{name: "auto candidate", text: strings.Repeat("a", 20), detection: Detection{Language: "auto", Reliable: true}, source: "auto", want: "auto", calls: 1},
		{name: "below length threshold", text: strings.Repeat("a", 19), detection: Detection{Language: "en"}, source: "auto", want: "auto", calls: 1},
		{name: "at length threshold", text: strings.Repeat("a", 20), detection: Detection{Language: "en"}, source: "auto", want: "en", calls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection {
				calls++
				return test.detection
			}))
			resolved, detection := identifier.ResolveSource(test.text, test.source)
			if resolved != test.want || calls != test.calls {
				t.Fatalf("ResolveSource() = %q, %+v; calls = %d", resolved, detection, calls)
			}
			if test.name == "unreliable usable auto" && (detection.Reliable || detection.Language != "de") {
				t.Fatalf("ResolveSource() changed detection metadata: %+v", detection)
			}
		})
	}
}

func TestLanguageIdentifierResolveSourceAmbiguousUkrainian(t *testing.T) {
	t.Parallel()
	const prose = "Сегодня хорошая погода, поэтому мы решили прогуляться по парку."
	type testCase struct {
		name     string
		text     string
		reliable bool
		want     string
	}
	tests := []testCase{
		{name: "unreliable prose", text: prose, want: "ru"},
		{name: "unreliable short text", text: "да", want: "ru"},
		{name: "Latin lowercase i", text: "да i", want: "ru"},
		{name: "Latin uppercase I", text: "да I", want: "ru"},
		{name: "reliable prose", text: prose, reliable: true, want: "uk"},
		{name: "reliable short text", text: "да", reliable: true, want: "uk"},
	}
	for _, character := range "іІїЇєЄґҐ" {
		tests = append(tests,
			testCase{name: "Ukrainian letter " + string(character), text: prose + string(character), want: "uk"},
			testCase{name: "short Ukrainian letter " + string(character), text: string(character), want: "auto"},
		)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wantDetection := Detection{Language: "uk", Confidence: 0.4, Reliable: test.reliable, Reason: DetectionReasonClassifierUnreliable}
			if test.reliable {
				wantDetection.Reason = DetectionReasonClassifier
			}
			identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection { return wantDetection }))
			resolved, detection := identifier.ResolveSource(test.text, "auto")
			if resolved != test.want || detection != wantDetection {
				t.Fatalf("ResolveSource(%q) = %q, %+v; want %q, %+v", test.text, resolved, detection, test.want, wantDetection)
			}
		})
	}
}

func TestLanguageIdentifierExplicitSourcesBypassAmbiguousUkrainian(t *testing.T) {
	t.Parallel()
	for _, source := range Supported() {
		if source.Code == "auto" {
			continue
		}
		t.Run(source.Code, func(t *testing.T) {
			identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection {
				t.Fatal("explicit source must bypass classification")
				return Detection{Language: "uk"}
			}))
			resolved, detection := identifier.ResolveSource("да", source.Code)
			if resolved != source.Code || detection != (Detection{}) {
				t.Fatalf("ResolveSource() = %q, %+v", resolved, detection)
			}
		})
	}
}

func TestLanguageIdentifierResolveSourceUnavailable(t *testing.T) {
	t.Parallel()
	for _, identifier := range []*LanguageIdentifier{nil, NewLanguageIdentifier(nil), NewLanguageIdentifier((*WhatlangClassifier)(nil))} {
		resolved, detection := identifier.ResolveSource("Сегодня хорошая погода, поэтому мы решили прогуляться по парку.", "auto")
		if resolved != "auto" || detection != (Detection{Reason: DetectionReasonUnavailable}) {
			t.Fatalf("ResolveSource() = %q, %+v", resolved, detection)
		}
	}
}

func TestLanguageIdentifierKeepsAutoForShortAmbiguousScripts(t *testing.T) {
	t.Parallel()
	identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection {
		return Detection{Language: "de", Confidence: 0.4, Reliable: false}
	}))
	for _, text := range []string{"OK", "Go", "test", "да", "нет", "USB", "root", "123"} {
		resolved, detection := identifier.ResolveSource(text, "auto")
		if resolved != "auto" || detection.Reliable {
			t.Fatalf("ResolveSource(%q) = %q, %+v", text, resolved, detection)
		}
	}
}

func TestLanguageIdentifierRejectsUnsupportedAndRecoversClassifierPanic(t *testing.T) {
	t.Parallel()
	unsupported := NewLanguageIdentifier(classifierFunc(func(string) Detection {
		return Detection{Language: "nl", Confidence: 1, Reliable: true}
	})).Detect("ambiguous text")
	if unsupported.Reliable || unsupported.Reason != DetectionReasonUnsupported {
		t.Fatalf("unsupported detection = %+v", unsupported)
	}

	panicking := NewLanguageIdentifier(classifierFunc(func(string) Detection { panic("backend failure") }))
	resolved, detection := panicking.ResolveSource("ambiguous text", "auto")
	if resolved != "auto" || detection.Reliable || detection.Reason != DetectionReasonUnavailable {
		t.Fatalf("panic fallback = %q, %+v", resolved, detection)
	}
}

func TestProductionCodeDoesNotReferenceLegacyDetector(t *testing.T) {
	t.Parallel()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	legacyName := []byte("Simple" + "Detector")
	for _, directory := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(projectRoot, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(contents, legacyName) {
				t.Errorf("legacy detector reference remains in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
