package translation

import "strings"

// NormalizeModelTranslation normalizes line endings returned by a model. A
// visible LF-only paragraph is ambiguous, so it is converted only when the
// source has a real paragraph break and the result has no real line breaks.
func NormalizeModelTranslation(text, sourceText string) string {
	text = normalizeRealLineEndings(text)
	normalizeVisibleLFParagraphs := sourceHasParagraphBreak(sourceText) && !strings.Contains(text, "\n")
	return normalizeVisibleLineBreaks(text, normalizeVisibleLFParagraphs)
}

// NormalizeImageTranslation also treats repeated visible LF sequences as
// paragraphs. Image translation has no source text for comparison, while a
// repeated unescaped LF is sufficiently distinct from a path's single escape.
func NormalizeImageTranslation(text string) string {
	return normalizeVisibleLineBreaks(normalizeRealLineEndings(text), true)
}

func normalizeRealLineEndings(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

func normalizeVisibleLineBreaks(text string, normalizeLFParagraphs bool) string {
	var normalized strings.Builder
	normalized.Grow(len(text))
	var quote byte
	for index := 0; index < len(text); {
		if (text[index] == '"' || text[index] == '`') && isUnescapedCharacter(text, index) {
			if quote == 0 {
				quote = text[index]
			} else if quote == text[index] {
				quote = 0
			}
		}
		if quote == 0 && strings.HasPrefix(text[index:], `\r\n`) && isUnescapedCharacter(text, index) {
			normalized.WriteByte('\n')
			index += len(`\r\n`)
			continue
		}
		if quote == 0 && normalizeLFParagraphs && strings.HasPrefix(text[index:], `\n`) && isUnescapedCharacter(text, index) {
			end := index
			lineBreaks := 0
			for strings.HasPrefix(text[end:], `\n`) && isUnescapedCharacter(text, end) {
				lineBreaks++
				end += len(`\n`)
			}
			if lineBreaks >= 2 {
				for range lineBreaks {
					normalized.WriteByte('\n')
				}
				index = end
				continue
			}
		}
		normalized.WriteByte(text[index])
		index++
	}
	return normalized.String()
}

func isUnescapedCharacter(text string, index int) bool {
	precedingBackslashes := 0
	for previous := index - 1; previous >= 0 && text[previous] == '\\'; previous-- {
		precedingBackslashes++
	}
	return precedingBackslashes%2 == 0
}

func sourceHasParagraphBreak(text string) bool {
	text = normalizeRealLineEndings(text)
	for index := 0; index < len(text); index++ {
		if text[index] != '\n' {
			continue
		}
		next := index + 1
		for next < len(text) && (text[next] == ' ' || text[next] == '\t') {
			next++
		}
		if next < len(text) && text[next] == '\n' {
			return true
		}
	}
	return false
}
