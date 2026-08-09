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

func (r *recordingRegexp) MatchString(s string) bool {
	r.compiler.mu.Lock()
	r.compiler.matches[r.source]++
	r.compiler.mu.Unlock()
	return r.re.MatchString(s)
}

func (r *recordingRegexp) FindStringIndex(s string) []int {
	r.compiler.mu.Lock()
	r.compiler.finds[r.source]++
	r.compiler.mu.Unlock()
	return r.re.FindStringIndex(s)
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

func TestCustomRegexBackendExcludedSurfacesUseStandardLibrary(t *testing.T) {
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
	if output == "" {
		t.Fatal("program produced no output")
	}
	if got := compiler.totalCompiles(); got != 0 {
		t.Fatalf("custom backend compiled %d excluded FS/RS/split/sub/gsub expressions, want 0", got)
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
