package translation

import "testing"

func TestNormalizeImageTranslation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "real CRLF and CR", input: "one\r\ntwo\rthree", want: "one\ntwo\nthree"},
		{name: "visible CRLF", input: `one\r\ntwo`, want: "one\ntwo"},
		{name: "visible paragraphs", input: `one\r\n\r\ntwo\r\nthree`, want: "one\n\ntwo\nthree"},
		{name: "visible LF paragraphs", input: `one\n\ntwo\n\nthree`, want: "one\n\ntwo\n\nthree"},
		{name: "LF only", input: "one\ntwo", want: "one\ntwo"},
		{name: "Windows path", input: `C:\react`, want: `C:\react`},
		{name: "double backslash path", input: `C:\\react`, want: `C:\\react`},
		{name: "double escaped CRLF", input: `one\\r\\ntwo`, want: `one\\r\\ntwo`},
		{name: "double escaped LF paragraphs", input: `one\\n\\ntwo`, want: `one\\n\\ntwo`},
		{name: "visible LF only", input: `folder\nsys`, want: `folder\nsys`},
		{name: "technical escapes", input: `^\w+\t\b\u1234\\$`, want: `^\w+\t\b\u1234\\$`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := NormalizeImageTranslation(test.input)
			if got != test.want {
				t.Fatalf("NormalizeImageTranslation(%q) = %q; want %q", test.input, got, test.want)
			}
			if second := NormalizeImageTranslation(got); second != got {
				t.Fatalf("normalization is not idempotent: first=%q second=%q", got, second)
			}
		})
	}
}

func TestNormalizeModelTranslation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		source string
		want   string
	}{
		{name: "real CRLF", input: "one\r\ntwo", want: "one\ntwo"},
		{name: "visible CRLF", input: `one\r\ntwo`, want: "one\ntwo"},
		{name: "visible CRLF paragraphs", input: `one\r\n\r\ntwo`, want: "one\n\ntwo"},
		{name: "visible LF paragraphs matching source", input: `one\n\ntwo`, source: "first\n\nsecond", want: "one\n\ntwo"},
		{name: "visible LF without source paragraphs", input: `one\n\ntwo`, source: "technical text", want: `one\n\ntwo`},
		{name: "Windows drive path", input: `C:\react`, source: "path", want: `C:\react`},
		{name: "Windows nested path", input: `D:\folder\nsys`, source: "path", want: `D:\folder\nsys`},
		{name: "Windows path with multiple escapes", input: `C:\new\test`, source: "path", want: `C:\new\test`},
		{name: "double backslash", input: `C:\\react`, source: "path", want: `C:\\react`},
		{name: "double escaped newline", input: `one\\n\\ntwo`, source: "first\n\nsecond", want: `one\\n\\ntwo`},
		{name: "technical escapes", input: `^\w+\t\b\u1234\\$`, source: "first\n\nsecond", want: `^\w+\t\b\u1234\\$`},
		{name: "JSON escaped paragraphs", input: `{"text":"one\n\ntwo"}`, source: "{\n\n\"text\":\"one\\n\\ntwo\"\n}", want: `{"text":"one\n\ntwo"}`},
		{name: "code string escaped paragraphs", input: `const text = "one\n\ntwo"`, source: "const value = 1\n\nconst text = \"one\\n\\ntwo\"", want: `const text = "one\n\ntwo"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := NormalizeModelTranslation(test.input, test.source)
			if got != test.want {
				t.Fatalf("NormalizeModelTranslation(%q, %q) = %q; want %q", test.input, test.source, got, test.want)
			}
			if second := NormalizeModelTranslation(got, test.source); second != got {
				t.Fatalf("normalization is not idempotent: first=%q second=%q", got, second)
			}
		})
	}
}
