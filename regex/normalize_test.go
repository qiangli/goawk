package regex_test

import (
	"testing"

	"github.com/benhoyt/goawk/regex"
)

func TestNormalizeSuccess(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"a{0002}", "a{2}"},
		{"a{0000}", "a{0}"},
		{"a{00255}", "a{255}"},
		{"a{0002,0005}", "a{2,5}"},
		{"a{0002,}", "a{2,}"},

		// Compositions of quantifiers
		{"a{0002}{0003}", "(?:a{2}){3}"},
		{"a{2}{3}{4,}", "(?:(?:a{2}){3}){4,}"},
		{"a{2,}{3}{4}", "(?:(?:a{2,}){3}){4}"},
		{"a{2,3}{4}", "(?:a{2,3}){4}"},
		{"a{2}{3,4}", "(?:a{2}){3,4}"},
		{"a*{2}", "(?:a*){2}"},
		{"a+{2}", "(?:a+){2}"},
		{"a?{2}", "(?:a?){2}"},

		// Groups & Alternations
		{"(a{0002}){0003}", "(a{2}){3}"},
		{"a{0002}|b{0003}", "a{2}|b{3}"},
		{"((a{0002})){0003}", "((a{2})){3}"},

		// Bracket expressions & nested atoms (braces preserved inside classes)
		{"[a{0002}]", "[a{0002}]"},
		{"[[:alpha:]0002{0003}]", "[[:alpha:]0002{0003}]"},
		{"[[.ch.]a{0002}]", "[[.ch.]a{0002}]"},
		{"[[=a=]{0002}]", "[[=a=]{0002}]"},
		{"[a-z[:digit:]]", "[a-z[:digit:]]"},
		{"[]a{0002}]", "[]a{0002}]"},
		{"[^]a{0002}]", "[^]a{0002}]"},

		// Escapes
		{"a\\{0002\\}", "a\\{0002\\}"},
		{"\\(a{0002}\\)", "\\(a{2}\\)"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := regex.Normalize(tt.input)
			if err != nil {
				t.Fatalf("Normalize(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeError(t *testing.T) {
	tests := []string{
		// Oversized bounds (>255)
		"a{256}",
		"a{000256}",
		"a{2,256}",
		"a{256,}",
		"a{999999999999999999999999999999}",
		"a{10000000000000000000,20000000000000000000}",

		// Oversized composition bounds (>255)
		"a{100}{3}",
		"a{200}{200}",

		// Malformed bounds & syntax
		"a{5,2}",
		"a{2,3,4}",
		"a{2,a}",
		"a{,2}",
		"a{-2}",
		"{2}",
		"a|{2}",
		"({2})",
		"a{2",
		"a\\",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := regex.Normalize(input)
			if err == nil {
				t.Fatalf("Normalize(%q) = %q, expected error", input, got)
			}
		})
	}
}
