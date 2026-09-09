package translation

import "unicode"

func SplitText(text string, maxRunes int) []string {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	if maxRunes < 1 {
		maxRunes = 1
	}
	parts := make([]string, 0, (len(runes)+maxRunes-1)/maxRunes)
	for len(runes) > maxRunes {
		cut := semanticCut(runes, maxRunes)
		parts = append(parts, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(runes) > 0 {
		parts = append(parts, string(runes))
	}
	return parts
}

func semanticCut(runes []rune, limit int) int {
	if limit >= len(runes) {
		return len(runes)
	}
	if cut := lastParagraphBoundary(runes, limit); cut > 0 {
		return cut
	}
	if cut := lastLineBoundary(runes, limit); cut > 0 {
		return cut
	}
	if cut := lastSentenceBoundary(runes, limit); cut > 0 {
		return cut
	}
	if cut := lastWhitespaceBoundary(runes, limit); cut > 0 {
		return cut
	}
	return limit
}

func lastParagraphBoundary(runes []rune, limit int) int {
	last := 0
	for index := 0; index < limit; {
		firstEnd, ok := lineBreakEnd(runes, index)
		if !ok {
			index++
			continue
		}
		secondStart := firstEnd
		for secondStart < limit && (runes[secondStart] == ' ' || runes[secondStart] == '\t') {
			secondStart++
		}
		secondEnd, ok := lineBreakEnd(runes, secondStart)
		if ok && secondEnd <= limit {
			last = secondEnd
			index = secondEnd
			continue
		}
		index = firstEnd
	}
	return last
}

func lastLineBoundary(runes []rune, limit int) int {
	last := 0
	for index := 0; index < limit; index++ {
		if end, ok := lineBreakEnd(runes, index); ok && end <= limit {
			last = end
			index = end - 1
		}
	}
	return last
}

func lineBreakEnd(runes []rune, index int) (int, bool) {
	if index >= len(runes) {
		return 0, false
	}
	switch runes[index] {
	case '\n':
		return index + 1, true
	case '\r':
		if index+1 < len(runes) && runes[index+1] == '\n' {
			return index + 2, true
		}
		return index + 1, true
	default:
		return 0, false
	}
}

func lastSentenceBoundary(runes []rune, limit int) int {
	last := 0
	for index := 0; index < limit; index++ {
		if !isSentenceTerminal(runes[index]) {
			continue
		}
		end := index + 1
		for end < limit && isClosingPunctuation(runes[end]) {
			end++
		}
		if end == len(runes) || end < len(runes) && unicode.IsSpace(runes[end]) {
			last = end
		}
	}
	return last
}

func isSentenceTerminal(value rune) bool {
	switch value {
	case '.', '!', '?', '。', '！', '？':
		return true
	default:
		return false
	}
}

func isClosingPunctuation(value rune) bool {
	switch value {
	case '"', '\'', ')', ']', '}', '»', '”', '’':
		return true
	default:
		return false
	}
}

func lastWhitespaceBoundary(runes []rune, limit int) int {
	last := 0
	for index := 0; index < limit; index++ {
		if unicode.IsSpace(runes[index]) {
			last = index + 1
		}
	}
	return last
}
