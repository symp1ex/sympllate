package localmodel

import "github.com/sympllate/translator/internal/translation"

func splitText(text string, maxRunes int) []string {
	return translation.SplitText(text, maxRunes)
}
