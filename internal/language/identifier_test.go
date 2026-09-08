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
		detection Detection
		source    string
		want      string
		calls     int
	}{
		{name: "reliable auto", detection: Detection{Language: "de", Reliable: true}, source: "auto", want: "de", calls: 1},
		{name: "unreliable auto", detection: Detection{Language: "de", Reliable: false}, source: "auto", want: "auto", calls: 1},
		{name: "explicit bypass", detection: Detection{Language: "de", Reliable: true}, source: "fr", want: "fr", calls: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection {
				calls++
				return test.detection
			}))
			resolved, detection := identifier.ResolveSource("ambiguous text", test.source)
			if resolved != test.want || calls != test.calls {
				t.Fatalf("ResolveSource() = %q, %+v; calls = %d", resolved, detection, calls)
			}
		})
	}
}

func TestLanguageIdentifierKeepsAutoForShortAmbiguousScripts(t *testing.T) {
	t.Parallel()
	identifier := NewLanguageIdentifier(classifierFunc(func(string) Detection {
		return Detection{Language: "de", Confidence: 0.4, Reliable: false}
	}))
	for _, text := range []string{"test", "тест"} {
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
