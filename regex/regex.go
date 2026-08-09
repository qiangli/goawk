package regex

import "regexp"

// Regexp is the interface for a compiled regular expression.
type Regexp interface {
	String() string
	MatchString(s string) bool
	FindStringIndex(s string) []int
}

// Compiler is a factory for compiling regular expressions.
type Compiler interface {
	Compile(expr string) (Regexp, error)
}

// defaultCompiler implements Compiler using the Go standard library regexp.
type defaultCompiler struct{}

func (defaultCompiler) Compile(expr string) (Regexp, error) {
	// GoAWK regexes require the "s" flag so '.' matches '\n' (like other AWKs)
	re, err := regexp.Compile("(?s:" + expr + ")")
	if err != nil {
		// Return the original compile error which might have a different offset
		// if we just blindly prepend, but actually GoAWK's original AddRegexFlags
		// just does "(?s:" + regex + ")" so it's identical to original behaviour.
		return nil, err
	}
	re.Longest() // other awks use leftmost-longest matching
	return re, nil
}

// DefaultCompiler is a regex.Compiler that uses the Go standard library.
var DefaultCompiler Compiler = defaultCompiler{}
