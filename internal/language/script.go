package language

import "unicode"

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
