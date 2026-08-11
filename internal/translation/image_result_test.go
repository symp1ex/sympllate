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
		{name: "LF only", input: "one\ntwo", want: "one\ntwo"},
		{name: "Windows path", input: `C:\react`, want: `C:\react`},
		{name: "double backslash path", input: `C:\\react`, want: `C:\\react`},
		{name: "double escaped CRLF", input: `one\\r\\ntwo`, want: `one\\r\\ntwo`},
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
