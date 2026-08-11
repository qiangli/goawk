package regex

import "regexp"

// Regexp is the regular expression surface used by all GoAWK expression,
// separator, splitting, and substitution operations. String must return a
// stable representation for diagnostics and disassembly.
type Regexp interface {
	String() string
	MatchString(s string) (bool, error)
	FindStringIndex(s string) ([]int, error)
	FindAllStringIndex(s string, n int) ([][]int, error)
	FindIndex(b []byte) ([]int, error)
	Split(s string, n int) ([]string, error)
	ReplaceAllStringFunc(s string, repl func(string) string) (string, error)
}

// Compiler compiles AWK extended regular expressions. Implementations must
// use leftmost-longest matching and allow period to match newline, matching
// the semantics of GoAWK's standard-library backend.
type Compiler interface {
	Compile(expr string) (Regexp, error)
}

// defaultCompiler implements Compiler using the Go standard library regexp.
type defaultCompiler struct{}

type defaultRegexp struct{ *regexp.Regexp }

func (r defaultRegexp) MatchString(s string) (bool, error) { return r.Regexp.MatchString(s), nil }
func (r defaultRegexp) FindStringIndex(s string) ([]int, error) {
	return r.Regexp.FindStringIndex(s), nil
}
func (r defaultRegexp) FindAllStringIndex(s string, n int) ([][]int, error) {
	return r.Regexp.FindAllStringIndex(s, n), nil
}
func (r defaultRegexp) FindIndex(b []byte) ([]int, error)       { return r.Regexp.FindIndex(b), nil }
func (r defaultRegexp) Split(s string, n int) ([]string, error) { return r.Regexp.Split(s, n), nil }
func (r defaultRegexp) ReplaceAllStringFunc(s string, repl func(string) string) (string, error) {
	return r.Regexp.ReplaceAllStringFunc(s, repl), nil
}

func (defaultCompiler) Compile(expr string) (Regexp, error) {
	norm, err := Normalize(expr)
	if err != nil {
		return nil, err
	}
	// GoAWK regexes require the "s" flag so '.' matches '\n' (like other AWKs)
	re, err := regexp.Compile("(?s:" + norm + ")")
	if err != nil {
		// Return the original compile error which might have a different offset
		// if we just blindly prepend, but actually GoAWK's original AddRegexFlags
		// just does "(?s:" + regex + ")" so it's identical to original behaviour.
		return nil, err
	}
	re.Longest() // other awks use leftmost-longest matching
	return defaultRegexp{re}, nil
}

// DefaultCompiler returns a Compiler backed by Go's regexp package.
func DefaultCompiler() Compiler {
	return defaultCompiler{}
}
