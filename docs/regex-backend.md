# Regular expression backend extension

GoAWK normally compiles AWK extended regular expressions with Go's `regexp`
package. Embedders can select another dependency-neutral backend by implementing
the interfaces in `github.com/benhoyt/goawk/regex` and passing the compiler to
`parser.ParseProgram`:

```go
program, err := parser.ParseProgram(source, &parser.ParserConfig{
	RegexCompiler: compiler,
})
```

The returned program retains that compiler. Literal expressions and dynamic
`~`, `!~`, and `match()` expressions therefore use one backend for the entire
program lifetime, including repeated `interp.Interpreter.Execute` calls. The
backend must implement AWK's leftmost-longest matching and dot-matches-newline
semantics. This first slice deliberately leaves FS, RS, `split`, `sub`, and
`gsub` on Go's standard-library backend.

## Coreutils adapter handoff

The coreutils adapter should live in coreutils, so GoAWK does not import
coreutils or create a dependency cycle. Its `regex.Compiler` implementation
should:

1. compile the expression with `pkg/bre` in ERE mode and dot-newline enabled;
2. call `Longest` on the resulting matcher;
3. wrap the matcher with the original expression so `String()` returns a stable
   source form; and
4. return that wrapper's `MatchString` and `FindStringIndex` methods through the
   GoAWK `regex.Regexp` interface.

The coreutils awk command then passes the adapter only through
`parser.ParserConfig.RegexCompiler`; `interp.ExecProgram` automatically uses
the compiler retained in the parsed program for dynamic expressions.
