package language

import "unicode"

// FallbackSourceLanguage returns a conservative supported language for text
// whose normal language identification did not produce a usable source.
func FallbackSourceLanguage(text string) string {
	var kana, hangul, arabic, cyrillic, ukrainian, han, latin bool
	latinCandidate := ""
	latinConflict := false
	for _, character := range text {
		if character == '¿' || character == '¡' {
			latin = true
			addLatinFallbackCandidate(&latinCandidate, &latinConflict, "es")
		}
		if !unicode.IsLetter(character) {
			continue
		}
		switch {
		case unicode.In(character, unicode.Hiragana, unicode.Katakana):
			kana = true
		case unicode.In(character, unicode.Hangul):
			hangul = true
		case unicode.In(character, unicode.Arabic):
			arabic = true
		case unicode.In(character, unicode.Cyrillic):
			cyrillic = true
			if isUkrainianSpecific(character) {
				ukrainian = true
			}
		case unicode.In(character, unicode.Han):
			han = true
		case unicode.In(character, unicode.Latin):
			latin = true
			if candidate := distinctiveLatinLanguage(character); candidate != "" {
				addLatinFallbackCandidate(&latinCandidate, &latinConflict, candidate)
			}
		}
	}

	switch {
	case kana:
		return "ja"
	case hangul:
		return "ko"
	case arabic:
		return "ar"
	case cyrillic && ukrainian:
		return "uk"
	case cyrillic:
		return "ru"
	case han:
		return "zh"
	case latin && latinCandidate != "" && !latinConflict:
		return latinCandidate
	default:
		return "en"
	}
}

func isUkrainianSpecific(character rune) bool {
	switch character {
	case 'Ґ', 'ґ', 'Є', 'є', 'І', 'і', 'Ї', 'ї':
		return true
	default:
		return false
	}
}

func distinctiveLatinLanguage(character rune) string {
	switch character {
	case 'ß', 'ẞ':
		return "de"
	case 'ñ', 'Ñ':
		return "es"
	case 'ą', 'Ą', 'ć', 'Ć', 'ę', 'Ę', 'ł', 'Ł', 'ń', 'Ń', 'ś', 'Ś', 'ź', 'Ź', 'ż', 'Ż':
		return "pl"
	case 'ã', 'Ã', 'õ', 'Õ':
		return "pt"
	case 'ğ', 'Ğ', 'ı', 'İ', 'ş', 'Ş':
		return "tr"
	case 'œ', 'Œ', 'ë', 'Ë', 'ï', 'Ï', 'ÿ', 'Ÿ':
		return "fr"
	case 'ì', 'Ì', 'ò', 'Ò':
		return "it"
	default:
		return ""
	}
}

func addLatinFallbackCandidate(current *string, conflict *bool, candidate string) {
	if *current == "" {
		*current = candidate
		return
	}
	if *current != candidate {
		*conflict = true
	}
}

func deterministicScriptLanguage(text string) string {
	var kana, hangul, arabic, han, other bool
	for _, character := range text {
		if !unicode.IsLetter(character) {
			continue
		}
		switch {
		case unicode.In(character, unicode.Hiragana, unicode.Katakana):
			kana = true
		case unicode.In(character, unicode.Hangul):
			hangul = true
		case unicode.In(character, unicode.Arabic):
			arabic = true
		case unicode.In(character, unicode.Han):
			han = true
		default:
			other = true
		}
	}

	switch {
	case kana && !hangul && !arabic && !other:
		return "ja"
	case hangul && !kana && !arabic && !other:
		return "ko"
	case arabic && !kana && !hangul && !han && !other:
		return "ar"
	default:
		return ""
	}
}
