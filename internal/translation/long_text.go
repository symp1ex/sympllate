package translation

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxChunkSplitDepth = 16

type LongTextOptions struct {
	RequestFits        func(context.Context, TranslateRequest) (bool, int, error)
	TranslateOnce      func(context.Context, TranslateRequest) (string, error)
	ContextOverflow    func(error) error
	SuggestedChunkSize func(string, int) int
}

func TranslateLongText(ctx context.Context, req TranslateRequest, options LongTextOptions) (string, error) {
	fits, tokenCount, err := options.RequestFits(ctx, req)
	if err != nil {
		return "", err
	}
	if !fits {
		if tokenCount == 0 {
			tokenCount = -1
		}
		return translateLongTextChunk(ctx, req, options, 0, tokenCount, nil)
	}

	text, err := options.TranslateOnce(ctx, req)
	if err == nil {
		return CleanResultForSource(text, req.Text), nil
	}
	if options.ContextOverflow == nil {
		return "", err
	}
	overflow := options.ContextOverflow(err)
	if overflow == nil {
		return "", err
	}
	return translateLongTextChunk(ctx, req, options, 0, 0, overflow)
}

func translateLongTextChunk(ctx context.Context, req TranslateRequest, options LongTextOptions, depth, tokenCount int, cause error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	prefix, source, suffix := outerWhitespace(req.Text)
	if source == "" {
		return req.Text, nil
	}
	req.Text = source

	if cause == nil && tokenCount == 0 {
		fits, measured, err := options.RequestFits(ctx, req)
		if err != nil {
			return "", err
		}
		if fits {
			text, translateErr := options.TranslateOnce(ctx, req)
			if translateErr == nil {
				return prefix + CleanResultForSource(text, source) + suffix, nil
			}
			if options.ContextOverflow == nil {
				return "", translateErr
			}
			overflow := options.ContextOverflow(translateErr)
			if overflow == nil {
				return "", translateErr
			}
			cause = overflow
			tokenCount = 0
		} else {
			tokenCount = measured
		}
	}

	if depth >= maxChunkSplitDepth || utf8.RuneCountInString(source) < 2 {
		if cause != nil {
			return "", cause
		}
		text, err := options.TranslateOnce(ctx, req)
		if err != nil {
			return "", err
		}
		return prefix + CleanResultForSource(text, source) + suffix, nil
	}

	if tokenCount < 0 {
		tokenCount = 0
	}
	parts := SplitText(source, options.SuggestedChunkSize(source, tokenCount))
	if len(parts) < 2 {
		if cause != nil {
			return "", cause
		}
		text, err := options.TranslateOnce(ctx, req)
		if err != nil {
			return "", err
		}
		return prefix + CleanResultForSource(text, source) + suffix, nil
	}

	var result strings.Builder
	result.Grow(len(req.Text))
	result.WriteString(prefix)
	for _, part := range parts {
		partRequest := req
		partRequest.Text = part
		translated, err := translateLongTextChunk(ctx, partRequest, options, depth+1, 0, nil)
		if err != nil {
			return "", err
		}
		result.WriteString(translated)
	}
	result.WriteString(suffix)
	return result.String(), nil
}

func SafeInputTokenBudget(contextSize, numPredict int) int {
	reservedOutput := numPredict
	if reservedOutput < 1 {
		reservedOutput = 1
	}
	if reservedOutput > contextSize/2 {
		reservedOutput = contextSize / 2
	}
	safetyMargin := contextSize / 16
	if safetyMargin < 32 {
		safetyMargin = 32
	}
	if safetyMargin > contextSize/4 {
		safetyMargin = contextSize / 4
	}
	budget := contextSize - reservedOutput - safetyMargin
	if budget < 1 {
		return 1
	}
	return budget
}

func SuggestedChunkRunes(text string, measuredTokens, inputBudget, requestOverhead int) int {
	runeCount := utf8.RuneCountInString(text)
	if runeCount < 2 {
		return 1
	}
	limit := runeCount / 2
	if measuredTokens > inputBudget {
		limit = runeCount * inputBudget / measuredTokens
		limit = limit * 9 / 10
	} else if measuredTokens == 0 {
		sourceBudget := inputBudget - requestOverhead
		if sourceBudget > 0 && len([]byte(text)) > sourceBudget {
			limit = runeCount * sourceBudget / len([]byte(text))
			limit = limit * 9 / 10
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit >= runeCount {
		limit = runeCount / 2
	}
	return limit
}

func outerWhitespace(value string) (prefix, text, suffix string) {
	runes := []rune(value)
	start := 0
	for start < len(runes) && unicode.IsSpace(runes[start]) {
		start++
	}
	end := len(runes)
	for end > start && unicode.IsSpace(runes[end-1]) {
		end--
	}
	return string(runes[:start]), string(runes[start:end]), string(runes[end:])
}
