package translation

import "strings"

// NormalizeImageTranslation normalizes only line endings emitted by image
// translation paths. It deliberately handles the model's exact visible CRLF
// spelling without unescaping any other backslash sequence.
func NormalizeImageTranslation(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	const visibleCRLF = `\r\n`
	if !strings.Contains(text, visibleCRLF) {
		return text
	}
	var normalized strings.Builder
	normalized.Grow(len(text))
	for index := 0; index < len(text); {
		if strings.HasPrefix(text[index:], visibleCRLF) && (index == 0 || text[index-1] != '\\') {
			normalized.WriteByte('\n')
			index += len(visibleCRLF)
			continue
		}
		normalized.WriteByte(text[index])
		index++
	}
	return normalized.String()
}
