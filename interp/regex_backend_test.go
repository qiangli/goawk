package interp_test

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/benhoyt/goawk/interp"
	"github.com/benhoyt/goawk/parser"
	awkregex "github.com/benhoyt/goawk/regex"
)

type recordingCompiler struct {
	mu       sync.Mutex
	compiles map[string]int
	matches  map[string]int
	finds    map[string]int
	reject   string
}

func newRecordingCompiler() *recordingCompiler {
	return &recordingCompiler{
		compiles: make(map[string]int),
		matches:  make(map[string]int),
		finds:    make(map[string]int),
	}
}

func (c *recordingCompiler) Compile(expr string) (awkregex.Regexp, error) {
	c.mu.Lock()
	c.compiles[expr]++
	c.mu.Unlock()
	if expr == c.reject {
		return nil, errors.New("recording backend rejected expression")
	}
	re, err := regexp.Compile("(?s:" + expr + ")")
	if err != nil {
		return nil, err
	}
	re.Longest()
	return &recordingRegexp{source: expr, re: re, compiler: c}, nil
}

func (c *recordingCompiler) counts(expr string) (compiles, matches, finds int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.compiles[expr], c.matches[expr], c.finds[expr]
}

func (c *recordingCompiler) totalCompiles() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, count := range c.compiles {
		total += count
	}
	return total
}

type recordingRegexp struct {
	source   string
	re       *regexp.Regexp
	compiler *recordingCompiler
}

func (r *recordingRegexp) String() string { return r.source }

func (r *recordingRegexp) MatchString(s string) (bool, error) {
	r.compiler.mu.Lock()
	r.compiler.matches[r.source]++
	r.compiler.mu.Unlock()
	return r.re.MatchString(s), nil
}

func (r *recordingRegexp) FindStringIndex(s string) ([]int, error) {
	r.compiler.mu.Lock()
	r.compiler.finds[r.source]++
	r.compiler.mu.Unlock()
	return r.re.FindStringIndex(s), nil
}

func (r *recordingRegexp) FindAllStringIndex(s string, n int) ([][]int, error) {
	return r.re.FindAllStringIndex(s, n), nil
}

func (r *recordingRegexp) FindIndex(b []byte) ([]int, error) { return r.re.FindIndex(b), nil }
func (r *recordingRegexp) Split(s string, n int) ([]string, error) {
	return r.re.Split(s, n), nil
}
func (r *recordingRegexp) ReplaceAllStringFunc(s string, repl func(string) string) (string, error) {
	return r.re.ReplaceAllStringFunc(s, repl), nil
}

func parseAndExec(t *testing.T, source, input string, compiler awkregex.Compiler) (string, error) {
	t.Helper()
	var parserConfig *parser.ParserConfig
	if compiler != nil {
		parserConfig = &parser.ParserConfig{RegexCompiler: compiler}
	}
	program, err := parser.ParseProgram([]byte(source), parserConfig)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	_, err = interp.ExecProgram(program, &interp.Config{
		Stdin:         strings.NewReader(input),
		Output:        &output,
		Error:         io.Discard,
		Environ:       []string{},
		NewlineOutput: interp.RawNewlineMode,
	})
	return output.String(), err
}

func TestCustomRegexBackendExpressionSlice(t *testing.T) {
	compiler := newRecordingCompiler()
	source := `/literal/ { print "literal" }
END {
	p = "dyn"
	print ("dynamic" ~ p)
	print ("other" !~ p)
	print match("xxmatchyy", "match")
}`
	output, err := parseAndExec(t, source, "literal\n", compiler)
	if err != nil {
		t.Fatal(err)
	}
	if want := "literal\n1\n1\n3\n"; output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}

	if compiles, matches, finds := compiler.counts("literal"); compiles != 2 || matches != 1 || finds != 0 {
		t.Errorf("literal calls = compile:%d match:%d find:%d, want 2,1,0", compiles, matches, finds)
	}
	if compiles, matches, finds := compiler.counts("dyn"); compiles != 1 || matches != 2 || finds != 0 {
		t.Errorf("dynamic calls = compile:%d match:%d find:%d, want 1,2,0", compiles, matches, finds)
	}
	if compiles, matches, finds := compiler.counts("match"); compiles != 1 || matches != 0 || finds != 1 {
		t.Errorf("match calls = compile:%d match:%d find:%d, want 1,0,1", compiles, matches, finds)
	}
}

func TestCustomRegexBackendCoversStandardSurfaces(t *testing.T) {
	compiler := newRecordingCompiler()
	source := `BEGIN { FS = "[,:]"; RS = ";+" }
{
	n = split("a,b:c", parts, "[,:]")
	s = "aaabbb"
	sub("a+", "x", s)
	gsub("b+", "y", s)
	print NF, n, s
}`
	output, err := parseAndExec(t, source, "one:two;;", compiler)
	if err != nil {
		t.Fatal(err)
	}
	if output != "2 3 xy\n" {
		t.Fatalf("output = %q, want %q", output, "2 3 xy\n")
	}
	for _, expr := range []string{"[,:]", ";+", "a+", "b+"} {
		if compiles, _, _ := compiler.counts(expr); compiles == 0 {
			t.Errorf("custom backend did not compile %q", expr)
		}
	}
}

func TestCustomRegexBackendStandardSurfaceErrors(t *testing.T) {
	programs := map[string]string{
		"FS":    `BEGIN { FS = "bad" }`,
		"RS":    `BEGIN { RS = "bad" }`,
		"split": `BEGIN { split("x", a, "bad") }`,
		"sub":   `BEGIN { s = "x"; sub("bad", "y", s) }`,
		"gsub":  `BEGIN { s = "x"; gsub("bad", "y", s) }`,
	}
	for name, source := range programs {
		t.Run(name, func(t *testing.T) {
			compiler := newRecordingCompiler()
			compiler.reject = "bad"
			_, err := parseAndExec(t, source, "", compiler)
			if err == nil || !strings.Contains(err.Error(), `invalid regex "bad": recording backend rejected expression`) {
				t.Fatalf("error = %v, want custom backend rejection", err)
			}
		})
	}
}

func TestCustomRegexBackendCompileErrors(t *testing.T) {
	compiler := newRecordingCompiler()
	compiler.reject = "bad"
	if _, err := parser.ParseProgram([]byte(`/bad/ { print }`), &parser.ParserConfig{RegexCompiler: compiler}); err == nil ||
		!strings.Contains(err.Error(), "recording backend rejected expression") {
		t.Fatalf("literal parse error = %v, want backend error", err)
	}

	_, err := parseAndExec(t, `BEGIN { p = "bad"; print ("x" ~ p) }`, "", compiler)
	if err == nil || !strings.Contains(err.Error(), `invalid regex "bad": recording backend rejected expression`) {
		t.Fatalf("dynamic execution error = %v, want backend error", err)
	}
}

type nilCompiler struct{}

func (nilCompiler) Compile(string) (awkregex.Regexp, error) { return nil, nil }

type runtimeErrorCompiler struct {
	expr string
	err  error
}

func (c runtimeErrorCompiler) Compile(expr string) (awkregex.Regexp, error) {
	re, err := regexp.Compile("(?s:" + expr + ")")
	if err != nil {
		return nil, err
	}
	re.Longest()
	return runtimeErrorRegexp{Regexp: re, fail: expr == c.expr, err: c.err}, nil
}

type runtimeErrorRegexp struct {
	*regexp.Regexp
	fail bool
	err  error
}

func (r runtimeErrorRegexp) runtimeError() error {
	if r.fail {
		return r.err
	}
	return nil
}
func (r runtimeErrorRegexp) MatchString(s string) (bool, error) {
	if err := r.runtimeError(); err != nil {
		return false, err
	}
	return r.Regexp.MatchString(s), nil
}
func (r runtimeErrorRegexp) FindStringIndex(s string) ([]int, error) {
	if err := r.runtimeError(); err != nil {
		return nil, err
	}
	return r.Regexp.FindStringIndex(s), nil
}
func (r runtimeErrorRegexp) FindAllStringIndex(s string, n int) ([][]int, error) {
	if err := r.runtimeError(); err != nil {
		return nil, err
	}
	return r.Regexp.FindAllStringIndex(s, n), nil
}
func (r runtimeErrorRegexp) FindIndex(b []byte) ([]int, error) {
	if err := r.runtimeError(); err != nil {
		return nil, err
	}
	return r.Regexp.FindIndex(b), nil
}
func (r runtimeErrorRegexp) Split(s string, n int) ([]string, error) {
	if err := r.runtimeError(); err != nil {
		return nil, err
	}
	return r.Regexp.Split(s, n), nil
}
func (r runtimeErrorRegexp) ReplaceAllStringFunc(s string, repl func(string) string) (string, error) {
	if err := r.runtimeError(); err != nil {
		return "", err
	}
	return r.Regexp.ReplaceAllStringFunc(s, repl), nil
}

func TestCustomRegexBackendRuntimeErrors(t *testing.T) {
	wantErr := errors.New("runtime matcher failed")
	tests := map[string]struct{ source, input, expr string }{
		"literal": {`/lit/ { print }`, "lit\n", "lit"},
		"match":   {`BEGIN { print match("x", "mat") }`, "", "mat"},
		"FS":      {`BEGIN { FS="fsep" } { print NF }`, "a fsep b\n", "fsep"},
		"RS":      {`BEGIN { RS="rsep+" } { print }`, "a rsep b", "rsep+"},
		"split":   {`BEGIN { split("a,b", a, "splitsep") }`, "", "splitsep"},
		"sub":     {`BEGIN { s="a"; sub("subsep", "x", s) }`, "", "subsep"},
		"gsub":    {`BEGIN { s="a"; gsub("gsubsep", "x", s) }`, "", "gsubsep"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseAndExec(t, tc.source, tc.input, runtimeErrorCompiler{expr: tc.expr, err: wantErr})
			if err == nil || !strings.Contains(err.Error(), wantErr.Error()) {
				t.Fatalf("error = %v, want runtime matcher error", err)
			}
		})
	}
}

func TestCustomRegexBackendRejectsNilRegexp(t *testing.T) {
	if _, err := parser.ParseProgram([]byte(`/literal/ { print }`), &parser.ParserConfig{RegexCompiler: nilCompiler{}}); err == nil ||
		!strings.Contains(err.Error(), "regex compiler returned nil") {
		t.Fatalf("literal parse error = %v, want nil-regexp error", err)
	}
	_, err := parseAndExec(t, `BEGIN { p = "dynamic"; print ("x" ~ p) }`, "", nilCompiler{})
	if err == nil || !strings.Contains(err.Error(), `invalid regex "dynamic": compiler returned nil`) {
		t.Fatalf("dynamic execution error = %v, want nil-regexp error", err)
	}
}

func TestCustomRegexBackendCacheLivesWithProgram(t *testing.T) {
	compiler := newRecordingCompiler()
	program, err := parser.ParseProgram(
		[]byte(`BEGIN { p = "dynamic"; print ("dynamic" ~ p) }`),
		&parser.ParserConfig{RegexCompiler: compiler},
	)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := interp.New(program)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var output bytes.Buffer
		_, err := runner.Execute(&interp.Config{
			Output:        &output,
			Error:         io.Discard,
			Environ:       []string{},
			NewlineOutput: interp.RawNewlineMode,
		})
		if err != nil {
			t.Fatal(err)
		}
		if output.String() != "1\n" {
			t.Fatalf("execution %d output = %q, want %q", i, output.String(), "1\n")
		}
	}
	if compiles, matches, _ := compiler.counts("dynamic"); compiles != 1 || matches != 2 {
		t.Fatalf("cached dynamic calls = compile:%d match:%d, want 1,2", compiles, matches)
	}
}

func TestDefaultRegexBackendCompatibility(t *testing.T) {
	source := `/a.+b/ { print match($0, "b+$"), ($0 ~ "^a") }`
	input := "axxb\n"
	defaultOutput, defaultErr := parseAndExec(t, source, input, nil)
	explicitOutput, explicitErr := parseAndExec(t, source, input, awkregex.DefaultCompiler())
	if defaultOutput != explicitOutput || errorString(defaultErr) != errorString(explicitErr) {
		t.Fatalf("default = (%q, %v), explicit default = (%q, %v)", defaultOutput, defaultErr, explicitOutput, explicitErr)
	}
	if defaultOutput != "4 1\n" || defaultErr != nil {
		t.Fatalf("default output = (%q, %v), want (%q, nil)", defaultOutput, defaultErr, "4 1\n")
	}

	_, defaultErr = parser.ParseProgram([]byte(`/[/ { print }`), nil)
	_, explicitErr = parser.ParseProgram([]byte(`/[/ { print }`), &parser.ParserConfig{RegexCompiler: awkregex.DefaultCompiler()})
	if errorString(defaultErr) != errorString(explicitErr) {
		t.Fatalf("default parse error = %q, explicit default = %q", errorString(defaultErr), errorString(explicitErr))
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
