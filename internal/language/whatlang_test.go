package language

import "testing"

func TestWhatlangClassifierWhitelistMatchesSupportedLanguages(t *testing.T) {
	t.Parallel()
	classifier, err := NewWhatlangClassifier()
	if err != nil {
		t.Fatal(err)
	}
	wantCount := 0
	for _, candidate := range Supported() {
		if candidate.Code == "auto" {
			continue
		}
		wantCount++
		mapped, ok := appCodeToWhatlang(candidate.Code)
		if !ok || !classifier.options.Whitelist[mapped] {
			t.Errorf("supported language %q is not whitelisted", candidate.Code)
		}
		if roundTrip, ok := whatlangToAppCode(mapped); !ok || roundTrip != candidate.Code {
			t.Errorf("mapping %q round trip = %q, %v", candidate.Code, roundTrip, ok)
		}
	}
	if len(classifier.options.Whitelist) != wantCount {
		t.Fatalf("whitelist size = %d, want %d", len(classifier.options.Whitelist), wantCount)
	}
	if _, ok := appCodeToWhatlang("auto"); ok {
		t.Fatal("auto must not map to a WhatlangGo candidate")
	}
	for _, code := range []string{"ru", "en", "de", "fr", "es", "uk", "pl", "it", "pt", "tr", "zh", "ja", "ko", "ar"} {
		if !IsSupported(code) {
			t.Errorf("required language %q is not supported", code)
		}
	}
}

func TestWhatlangClassifierRepresentativeText(t *testing.T) {
	t.Parallel()
	classifier, err := NewWhatlangClassifier()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "english", text: "The quick brown fox jumps over the lazy dog while the children are playing in the garden.", want: "en"},
		{name: "russian", text: "Сегодня хорошая погода, поэтому мы решили прогуляться по большому городскому парку.", want: "ru"},
		{name: "german", text: "Heute ist das Wetter schön, deshalb machen wir einen langen Spaziergang durch den Stadtpark.", want: "de"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			detection := classifier.Detect(test.text)
			if detection.Language != test.want || !detection.Reliable {
				t.Fatalf("Detect(%q) = %+v, want reliable %q", test.text, detection, test.want)
			}
		})
	}
}

func TestWhatlangClassifierDoesNotTreatHanAsUnambiguousChinese(t *testing.T) {
	t.Parallel()
	classifier, err := NewWhatlangClassifier()
	if err != nil {
		t.Fatal(err)
	}
	detection := classifier.Detect("这是一个用于检查中文语言识别结果的完整句子。")
	if detection.Language != "zh" || detection.Reliable {
		t.Fatalf("Han detection = %+v, want an unreliable zh candidate", detection)
	}
}

func TestWhatlangClassifierShortAmbiguousTextIsSafe(t *testing.T) {
	t.Parallel()
	classifier, err := NewWhatlangClassifier()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", "   ", "test"} {
		detection := classifier.Detect(text)
		if detection.Reliable && !IsSupported(detection.Language) {
			t.Fatalf("Detect(%q) returned unsupported reliable result: %+v", text, detection)
		}
	}
}
