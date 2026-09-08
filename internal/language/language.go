package language

type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var supported = []Language{
	{Code: "auto", Name: "Auto-detect"}, {Code: "ru", Name: "Russian"}, {Code: "en", Name: "English"},
	{Code: "de", Name: "German"}, {Code: "fr", Name: "French"}, {Code: "es", Name: "Spanish"},
	{Code: "uk", Name: "Ukrainian"}, {Code: "pl", Name: "Polish"}, {Code: "it", Name: "Italian"},
	{Code: "pt", Name: "Portuguese"}, {Code: "tr", Name: "Turkish"}, {Code: "zh", Name: "Chinese"},
	{Code: "ja", Name: "Japanese"}, {Code: "ko", Name: "Korean"}, {Code: "ar", Name: "Arabic"},
}

func Supported() []Language { return append([]Language(nil), supported...) }

func IsSupported(code string) bool {
	for _, candidate := range supported {
		if candidate.Code == code {
			return true
		}
	}
	return false
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

func NonCollidingTarget(source, target, first, second string) string {
	if source != target {
		return target
	}
	if source == second {
		return first
	}
	return second
}
