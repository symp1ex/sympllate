package language

import (
	"strings"
	"unicode"
)

const minimumUnreliableCandidateLetters = 20

type DetectionReason string

const (
	DetectionReasonEmpty                DetectionReason = "empty"
	DetectionReasonScript               DetectionReason = "script"
	DetectionReasonClassifier           DetectionReason = "classifier"
	DetectionReasonClassifierUnreliable DetectionReason = "classifier_unreliable"
	DetectionReasonUnsupported          DetectionReason = "unsupported"
	DetectionReasonUnavailable          DetectionReason = "unavailable"
)

type Detection struct {
	Language   string
	Confidence float64
	Reliable   bool
	Reason     DetectionReason
}

type Classifier interface {
	Detect(text string) Detection
}

type LanguageIdentifier struct {
	classifier Classifier
}

func NewLanguageIdentifier(classifier Classifier) *LanguageIdentifier {
	return &LanguageIdentifier{classifier: classifier}
}

func (i *LanguageIdentifier) Detect(text string) (detection Detection) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Detection{Reason: DetectionReasonEmpty}
	}
	if language := deterministicScriptLanguage(text); language != "" {
		return Detection{Language: language, Confidence: 1, Reliable: true, Reason: DetectionReasonScript}
	}
	if i == nil || i.classifier == nil {
		return Detection{Reason: DetectionReasonUnavailable}
	}

	defer func() {
		if recover() != nil {
			detection = Detection{Reason: DetectionReasonUnavailable}
		}
	}()
	detection = i.classifier.Detect(text)
	if detection.Language == "" {
		detection.Reliable = false
		if detection.Reason == "" {
			detection.Reason = DetectionReasonClassifierUnreliable
		}
		return detection
	}
	if detection.Language == "auto" || !IsSupported(detection.Language) {
		detection.Reliable = false
		detection.Reason = DetectionReasonUnsupported
		return detection
	}
	if detection.Reason == "" {
		if detection.Reliable {
			detection.Reason = DetectionReasonClassifier
		} else {
			detection.Reason = DetectionReasonClassifierUnreliable
		}
	}
	return detection
}

func (i *LanguageIdentifier) ResolveSource(text, source string) (string, Detection) {
	if source != "auto" {
		return source, Detection{}
	}
	detection := i.Detect(text)
	if usableSourceCandidate(text, detection) {
		return detection.Language, detection
	}
	return "auto", detection
}

func usableSourceCandidate(text string, detection Detection) bool {
	if detection.Language == "" || detection.Language == "auto" || !IsSupported(detection.Language) {
		return false
	}
	if detection.Reliable {
		return true
	}

	// The classifier ranks letter trigrams. Requiring a conservative amount of
	// letter content keeps short labels, identifiers, and numbers on the true
	// auto fallback while allowing a supported low-confidence candidate for
	// normal prose and prose mixed with technical syntax.
	letters := 0
	for _, character := range text {
		if unicode.IsLetter(character) {
			letters++
			if letters >= minimumUnreliableCandidateLetters {
				return true
			}
		}
	}
	return false
}
