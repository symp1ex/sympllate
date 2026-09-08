package localmodel

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitTextUsesSemanticBoundariesAndPreservesInput(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		want  []string
	}{
		{
			name:  "paragraphs",
			text:  "Первый.\n\nВторой.\n\nТретий.",
			limit: 11,
			want:  []string{"Первый.\n\n", "Второй.\n\n", "Третий."},
		},
		{
			name:  "long paragraph prefers sentences",
			text:  "First sentence. Second sentence. Third sentence.",
			limit: 31,
			want:  []string{"First sentence.", " Second sentence.", " Third sentence."},
		},
		{
			name:  "long sentence prefers whitespace",
			text:  "one two three four five six",
			limit: 10,
			want:  []string{"one two ", "three ", "four five ", "six"},
		},
		{
			name:  "line boundaries",
			text:  "line one\r\nline two\nline three",
			limit: 18,
			want:  []string{"line one\r\n", "line two\n", "line three"},
		},
		{
			name:  "unicode rune fallback",
			text:  "Привет世界🙂абв",
			limit: 4,
			want:  []string{"Прив", "ет世界", "🙂абв"},
		},
		{
			name:  "no separators",
			text:  "abcdefghij",
			limit: 3,
			want:  []string{"abc", "def", "ghi", "j"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := splitText(test.text, test.limit)
			if strings.Join(got, "") != test.text {
				t.Fatalf("split + join = %q, want %q", strings.Join(got, ""), test.text)
			}
			if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Fatalf("splitText() = %#v, want %#v", got, test.want)
			}
			for _, part := range got {
				if !utf8.ValidString(part) || utf8.RuneCountInString(part) > test.limit {
					t.Fatalf("invalid part %q (runes=%d)", part, utf8.RuneCountInString(part))
				}
			}
		})
	}
}

func TestSplitTextPreservesBlankLinesAndOuterWhitespace(t *testing.T) {
	t.Parallel()
	text := "  heading\n\n\nparagraph one\nparagraph two\n\ntrailer  \n"
	parts := splitText(text, 18)
	if got := strings.Join(parts, ""); got != text {
		t.Fatalf("split + join = %q, want %q", got, text)
	}
}
