package language

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
)

type WhatlangClassifier struct {
	options whatlanggo.Options
}

func NewWhatlangClassifier() (*WhatlangClassifier, error) {
	whitelist := make(map[whatlanggo.Lang]bool, len(supported))
	for _, language := range Supported() {
		if language.Code == "auto" {
			continue
		}
		candidate, ok := appCodeToWhatlang(language.Code)
		if !ok {
			return nil, fmt.Errorf("WhatlangGo does not map supported language %q", language.Code)
		}
		whitelist[candidate] = true
	}
	return &WhatlangClassifier{options: whatlanggo.Options{Whitelist: whitelist}}, nil
}

func (c *WhatlangClassifier) Detect(text string) Detection {
	text = strings.TrimSpace(text)
	if text == "" {
		return Detection{Reason: DetectionReasonEmpty}
	}
	if c == nil {
		return Detection{Reason: DetectionReasonUnavailable}
	}
	info := whatlanggo.DetectWithOptions(text, c.options)
	code, ok := whatlangToAppCode(info.Lang)
	if !ok {
		return Detection{Confidence: info.Confidence, Reason: DetectionReasonUnsupported}
	}
	reliable := info.IsReliable()
	if info.Script == unicode.Han && IsSupported("zh") && IsSupported("ja") {
		reliable = false
	}
	reason := DetectionReasonClassifierUnreliable
	if reliable {
		reason = DetectionReasonClassifier
	}
	return Detection{Language: code, Confidence: info.Confidence, Reliable: reliable, Reason: reason}
}

func appCodeToWhatlang(code string) (whatlanggo.Lang, bool) {
	switch code {
	case "ru":
		return whatlanggo.Rus, true
	case "en":
		return whatlanggo.Eng, true
	case "de":
		return whatlanggo.Deu, true
	case "fr":
		return whatlanggo.Fra, true
	case "es":
		return whatlanggo.Spa, true
	case "uk":
		return whatlanggo.Ukr, true
	case "pl":
		return whatlanggo.Pol, true
	case "it":
		return whatlanggo.Ita, true
	case "pt":
		return whatlanggo.Por, true
	case "tr":
		return whatlanggo.Tur, true
	case "zh":
		return whatlanggo.Cmn, true
	case "ja":
		return whatlanggo.Jpn, true
	case "ko":
		return whatlanggo.Kor, true
	case "ar":
		return whatlanggo.Arb, true
	default:
		return 0, false
	}
}

func whatlangToAppCode(candidate whatlanggo.Lang) (string, bool) {
	for _, language := range Supported() {
		mapped, ok := appCodeToWhatlang(language.Code)
		if ok && mapped == candidate {
			return language.Code, true
		}
	}
	return "", false
}
