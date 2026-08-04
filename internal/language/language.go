package language

import (
	"strings"
	"unicode"
)

type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var supported = []Language{
	{Code: "auto", Name: "Автоопределение"}, {Code: "ru", Name: "Русский"}, {Code: "en", Name: "Английский"},
	{Code: "de", Name: "Немецкий"}, {Code: "fr", Name: "Французский"}, {Code: "es", Name: "Испанский"},
	{Code: "uk", Name: "Украинский"}, {Code: "pl", Name: "Польский"}, {Code: "it", Name: "Итальянский"},
	{Code: "pt", Name: "Португальский"}, {Code: "tr", Name: "Турецкий"}, {Code: "zh", Name: "Китайский"},
	{Code: "ja", Name: "Японский"}, {Code: "ko", Name: "Корейский"}, {Code: "ar", Name: "Арабский"},
}

func Supported() []Language { return append([]Language(nil), supported...) }

type Detector interface{ Detect(text string) string }

type SimpleDetector struct{}

func (SimpleDetector) Detect(text string) string {
	lower := strings.ToLower(text)
	counts := map[string]int{}
	letters := 0
	for _, r := range lower {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			counts["ja"] += 3
		case unicode.In(r, unicode.Hangul):
			counts["ko"] += 3
		case unicode.In(r, unicode.Han):
			counts["zh"] += 2
		case unicode.In(r, unicode.Arabic):
			counts["ar"] += 3
		case unicode.In(r, unicode.Cyrillic):
			counts["ru"] += 2
		case unicode.In(r, unicode.Latin):
			counts["en"]++
		}
	}
	if letters == 0 {
		return ""
	}
	if strings.ContainsAny(lower, "іїєґ") {
		return "uk"
	}
	if strings.ContainsAny(lower, "¿¡ñ") {
		counts["es"] += 40
	}
	if strings.ContainsAny(lower, "ąćęłńśźż") {
		counts["pl"] += 40
	}
	for code, chars := range map[string]string{
		"de": "äöüß", "fr": "àâæçéèêëîïôœùûüÿ", "es": "áéíóúüñ¿¡", "pl": "ąćęłńóśźż",
		"pt": "ãõáâàçéêíóôú", "tr": "çğıöşü", "it": "àèéìíîòóùú",
	} {
		if strings.ContainsAny(lower, chars) {
			counts[code] += 20
		}
	}
	for code, words := range map[string][]string{
		"de": {" der ", " die ", " und ", " nicht "}, "fr": {" le ", " la ", " et ", " une "},
		"es": {" el ", " los ", " que ", " una "}, "it": {" il ", " che ", " non ", " una "},
		"pt": {" não ", " que ", " uma ", " para "}, "tr": {" bir ", " ve ", " için "}, "pl": {" nie ", " jest ", " oraz "},
	} {
		padded := " " + lower + " "
		for _, word := range words {
			if strings.Contains(padded, word) {
				counts[code] += 3
			}
		}
	}
	best, score := "", 0
	for code, value := range counts {
		if value > score {
			best, score = code, value
		}
	}
	if score < 2 {
		return ""
	}
	return best
}

type Direction struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Detected string `json:"detectedLanguage,omitempty"`
}

func ChooseDirection(detected, first, second, fallback string) Direction {
	switch detected {
	case first:
		return Direction{Source: first, Target: second, Detected: detected}
	case second:
		return Direction{Source: second, Target: first, Detected: detected}
	case "":
		return Direction{Source: "auto", Target: fallback}
	default:
		return Direction{Source: detected, Target: fallback, Detected: detected}
	}
}
