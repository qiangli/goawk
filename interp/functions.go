// Call native Go functions; helpers for some builtin function calls.

package interp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/benhoyt/goawk/internal/resolver"
	"github.com/benhoyt/goawk/lexer"
)

// Call native-defined function with given name and arguments, return
// its return value (or null value if it doesn't return anything).
func (p *interp) callNative(index int, args []value) (value, error) {
	f := p.nativeFuncs[index]
	minIn := len(f.in) // Minimum number of args we should pass
	var variadicType reflect.Type
	if f.isVariadic {
		variadicType = f.in[len(f.in)-1].Elem()
		minIn--
	}

	// Build list of args to pass to function
	values := make([]reflect.Value, 0, 7) // up to 7 args won't require heap allocation
	for i, a := range args {
		var argType reflect.Type
		if !f.isVariadic || i < len(f.in)-1 {
			argType = f.in[i]
		} else {
			// Final arg(s) when calling a variadic are all of this type
			argType = variadicType
		}
		values = append(values, p.toNative(a, argType))
	}
	// Use zero value for any unspecified args
	for i := len(args); i < minIn; i++ {
		values = append(values, reflect.Zero(f.in[i]))
	}

	// Call Go function, determine return value
	outs := f.value.Call(values)
	switch len(outs) {
	case 0:
		// No return value, return null value to AWK
		return null(), nil
	case 1:
		// Single return value
		return fromNative(outs[0]), nil
	case 2:
		// Two-valued return of (scalar, error)
		if !outs[1].IsNil() {
			return null(), outs[1].Interface().(error)
		}
		return fromNative(outs[0]), nil
	default:
		// Should never happen (checked at parse time)
		panic(fmt.Sprintf("unexpected number of return values: %d", len(outs)))
	}
}

// Convert from an AWK value to a native Go value
func (p *interp) toNative(v value, typ reflect.Type) reflect.Value {
	switch typ.Kind() {
	case reflect.Bool:
		return reflect.ValueOf(v.boolean())
	case reflect.Int:
		return reflect.ValueOf(int(v.num()))
	case reflect.Int8:
		return reflect.ValueOf(int8(v.num()))
	case reflect.Int16:
		return reflect.ValueOf(int16(v.num()))
	case reflect.Int32:
		return reflect.ValueOf(int32(v.num()))
	case reflect.Int64:
		return reflect.ValueOf(int64(v.num()))
	case reflect.Uint:
		return reflect.ValueOf(uint(int64(v.num())))
	case reflect.Uint8:
		return reflect.ValueOf(uint8(int64(v.num())))
	case reflect.Uint16:
		return reflect.ValueOf(uint16(int64(v.num())))
	case reflect.Uint32:
		return reflect.ValueOf(uint32(int64(v.num())))
	case reflect.Uint64:
		return reflect.ValueOf(uint64(int64(v.num())))
	case reflect.Float32:
		return reflect.ValueOf(float32(v.num()))
	case reflect.Float64:
		return reflect.ValueOf(v.num())
	case reflect.String:
		return reflect.ValueOf(p.toString(v))
	case reflect.Slice:
		if typ.Elem().Kind() != reflect.Uint8 {
			// Shouldn't happen: prevented by checkNativeFunc
			panic(fmt.Sprintf("unexpected argument slice: %s", typ.Elem().Kind()))
		}
		return reflect.ValueOf([]byte(p.toString(v)))
	default:
		// Shouldn't happen: prevented by checkNativeFunc
		panic(fmt.Sprintf("unexpected argument type: %s", typ.Kind()))
	}
}

// Convert from a native Go value to an AWK value
func fromNative(v reflect.Value) value {
	switch v.Kind() {
	case reflect.Bool:
		return boolean(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return num(float64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return num(float64(v.Uint()))
	case reflect.Float32, reflect.Float64:
		return num(v.Float())
	case reflect.String:
		return str(v.String())
	case reflect.Slice:
		if b, ok := v.Interface().([]byte); ok {
			return str(string(b))
		}
		// Shouldn't happen: prevented by checkNativeFunc
		panic(fmt.Sprintf("unexpected return slice: %s", v.Type().Elem().Kind()))
	default:
		// Shouldn't happen: prevented by checkNativeFunc
		panic(fmt.Sprintf("unexpected return type: %s", v.Kind()))
	}
}

// Used for caching native function type information on init
type nativeFunc struct {
	isVariadic bool
	in         []reflect.Type
	value      reflect.Value
}

// Check and initialize native functions
func (p *interp) initNativeFuncs(funcs map[string]any) error {
	for name, f := range funcs {
		err := checkNativeFunc(name, f)
		if err != nil {
			return err
		}
	}

	// Sort functions by name, then use those indexes to build slice
	// (this has to match how the parser sets the indexes).
	names := make([]string, 0, len(funcs))
	for name := range funcs {
		names = append(names, name)
	}
	sort.Strings(names)
	p.nativeFuncs = make([]nativeFunc, len(names))
	for i, name := range names {
		f := funcs[name]
		typ := reflect.TypeOf(f)
		in := make([]reflect.Type, typ.NumIn())
		for j := 0; j < len(in); j++ {
			in[j] = typ.In(j)
		}
		p.nativeFuncs[i] = nativeFunc{
			isVariadic: typ.IsVariadic(),
			in:         in,
			value:      reflect.ValueOf(f),
		}
	}
	return nil
}

// Got this trick from the Go stdlib text/template source
var errorType = reflect.TypeOf((*error)(nil)).Elem()

// Check that native function with given name is okay to call from
// AWK, return an *interp.Error if not. This checks that f is actually
// a function, and that its parameter and return types are good.
func checkNativeFunc(name string, f any) error {
	if lexer.KeywordToken(name) != lexer.ILLEGAL {
		return newError("can't use keyword %q as native function name", name)
	}

	typ := reflect.TypeOf(f)
	if typ.Kind() != reflect.Func {
		return newError("native function %q is not a function", name)
	}
	for i := 0; i < typ.NumIn(); i++ {
		param := typ.In(i)
		if typ.IsVariadic() && i == typ.NumIn()-1 {
			param = param.Elem()
		}
		if !validNativeType(param) {
			return newError("native function %q param %d is not int or string", name, i)
		}
	}

	switch typ.NumOut() {
	case 0:
		// No return value is fine
	case 1:
		// Single scalar return value is fine
		if !validNativeType(typ.Out(0)) {
			return newError("native function %q return value is not int or string", name)
		}
	case 2:
		// Returning (scalar, error) is handled too
		if !validNativeType(typ.Out(0)) {
			return newError("native function %q first return value is not int or string", name)
		}
		if typ.Out(1) != errorType {
			return newError("native function %q second return value is not an error", name)
		}
	default:
		return newError("native function %q returns more than two values", name)
	}
	return nil
}

// Return true if typ is a valid parameter or return type.
func validNativeType(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Bool:
		return true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		return true
	case reflect.String:
		return true
	case reflect.Slice:
		// Only allow []byte (convert to string in AWK)
		return typ.Elem().Kind() == reflect.Uint8
	default:
		return false
	}
}

// Guts of the split() function
func (p *interp) split(s string, scope resolver.Scope, index int, sep string, sepIsRegex bool, mode IOMode) (int, error) {
	var parts []string
	switch {
	case mode == CSVMode || mode == TSVMode:
		// Set up for parsing a CSV/TSV record
		splitter := csvSplitter{
			separator: p.csvInputConfig.Separator,
			sepLen:    utf8.RuneLen(p.csvInputConfig.Separator),
			comment:   p.csvInputConfig.Comment,
			fields:    &parts,
		}
		scanner := bufio.NewScanner(strings.NewReader(s))
		scanner.Split(splitter.scan)
		if p.splitBuffer == nil {
			p.splitBuffer = make([]byte, inputBufSize)
		}
		scanner.Buffer(p.splitBuffer, maxRecordLength)

		// Parse one record. Errors shouldn't happen, but if there is one,
		// len(parts) will be 0.
		scanner.Scan()
	case !sepIsRegex && sep == " ":
		parts = strings.Fields(s)
	case s == "":
		// Leave parts 0 length on empty string
	case !sepIsRegex && utf8.RuneCountInString(sep) <= 1:
		parts = strings.Split(s, sep)
	default:
		re, err := p.compileRegexStd(sep)
		if err != nil {
			return 0, err
		}
		parts = re.Split(s, -1)
	}
	array := make(map[string]value, len(parts))
	for i, part := range parts {
		array[strconv.Itoa(i+1)] = numStr(part)
	}
	p.arrays[p.arrayIndex(scope, index)] = array
	return len(array), nil
}

// Guts of the sub() and gsub() functions
func (p *interp) sub(regex, repl, in string, global bool) (out string, num int, err error) {
	re, err := p.compileRegexStd(regex)
	if err != nil {
		return "", 0, err
	}
	count := 0
	out = re.ReplaceAllStringFunc(in, func(s string) string {
		// Only do the first replacement for sub(), or all for gsub()
		if !global && count > 0 {
			return s
		}
		count++
		// Handle & (ampersand) properly in replacement string
		r := make([]byte, 0, 64) // Up to 64 byte replacement won't require heap allocation
		for i := 0; i < len(repl); i++ {
			switch repl[i] {
			case '&':
				r = append(r, s...)
			case '\\':
				i++
				if i < len(repl) {
					switch repl[i] {
					case '&':
						r = append(r, '&')
					case '\\':
						r = append(r, '\\')
					default:
						r = append(r, '\\', repl[i])
					}
				} else {
					r = append(r, '\\')
				}
			default:
				r = append(r, repl[i])
			}
		}
		return string(r)
	})
	return out, count, nil
}

type cachedFormat struct {
	format string
	types  []byte
}

// POSIX specifies that a negative precision supplied by * is treated as if
// the precision were omitted. Go's fmt package instead emits %!(BADPREC), so
// remove the precision and its argument before handing the format to fmt.
func normalizeNegativeDynamicPrecisions(format string, args []value) (string, []value) {
	if !strings.Contains(format, ".*") {
		return format, args
	}
	out := make([]byte, 0, len(format))
	drop := make([]bool, len(args))
	argIndex := 0
	changed := false
	for i := 0; i < len(format); {
		out = append(out, format[i])
		if format[i] != '%' {
			i++
			continue
		}
		i++
		if i >= len(format) {
			break
		}
		out = append(out, format[i])
		if format[i] == '%' {
			i++
			continue
		}
		for bytes.IndexByte([]byte(" .-+*#0123456789"), format[i]) >= 0 {
			if format[i] == '*' {
				isPrecision := i > 0 && format[i-1] == '.'
				if isPrecision && argIndex < len(args) && int64(args[argIndex].num()) < 0 {
					out = out[:len(out)-2] // remove the copied .* pair
					drop[argIndex] = true
					changed = true
				}
				argIndex++
			}
			i++
			if i >= len(format) {
				break
			}
			out = append(out, format[i])
		}
		if i < len(format) {
			argIndex++ // conversion operand
			i++
		}
	}
	if !changed {
		return format, args
	}
	filtered := make([]value, 0, len(args))
	for i, arg := range args {
		if !drop[i] {
			filtered = append(filtered, arg)
		}
	}
	return string(out), filtered
}

// normalizeOctalHashZeroPrecision works around a divergence between Go's fmt
// package and C/POSIX printf for the "%#o" conversion. POSIX specifies that
// the '#' flag forces the first octal digit to be zero, and that when both the
// value and the precision are zero a single "0" is still printed. Go's fmt
// instead yields an empty string for "%#.0o" (and "%#.*o" with a zero
// precision argument) when the value is zero. Rewriting an explicit zero
// precision to 1 reproduces the POSIX result for a zero value while leaving
// non-zero values (and every other verb) untouched; because a precision is
// still specified, the '0' (zero-pad) flag stays suppressed as POSIX requires.
// Plain "%.0o" (no '#') is deliberately left alone so it keeps emitting the
// empty string mandated by POSIX.
func normalizeOctalHashZeroPrecision(format string, args []value) (string, []value) {
	if !strings.Contains(format, "#") {
		return format, args
	}
	out := []byte(format)
	drop := make([]bool, len(args))
	argIndex := 0
	changed := false
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i >= len(format) {
			break
		}
		if format[i] == '%' {
			continue
		}
		hash := false
		precZeroPos := -1  // index in out of a static ".0" precision digit
		precStarPos := -1  // index in out of a dynamic ".*" precision
		precStarArg := -1
		for i < len(format) && strings.IndexByte(" .-+*#0123456789", format[i]) >= 0 {
			switch {
			case format[i] == '#':
				hash = true
			case format[i] == '*':
				if i > 0 && format[i-1] == '.' {
					precStarPos = i
					precStarArg = argIndex
				}
				argIndex++
			case i > 0 && format[i-1] == '.' && format[i] == '0':
				precZeroPos = i
			default:
				precZeroPos = -1 // any other precision digit is non-zero
			}
			i++
		}
		if i >= len(format) {
			break
		}
		verb := format[i]
		argIndex++ // conversion operand
		if hash && verb == 'o' {
			switch {
			case precZeroPos >= 0:
				out[precZeroPos] = '1'
				changed = true
			case precStarPos >= 0 && precStarArg < len(args) && int64(args[precStarArg].num()) == 0:
				out[precStarPos] = '1'
				drop[precStarArg] = true
				changed = true
			}
		}
	}
	if !changed {
		return format, args
	}
	filtered := make([]value, 0, len(args))
	for i, arg := range args {
		if !drop[i] {
			filtered = append(filtered, arg)
		}
	}
	return string(out), filtered
}

// Parse given sprintf format string into Go format string, along with
// type conversion specifiers. Output is memoized in a simple cache
// for performance.
func (p *interp) parseFmtTypes(s string) (format string, types []byte, err error) {
	if item, ok := p.formatCache[s]; ok {
		return item.format, item.types, nil
	}

	out := []byte(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '%' {
			i++
			if i >= len(s) {
				return "", nil, errors.New("expected type specifier after %")
			}
			if s[i] == '%' {
				continue
			}
			for i < len(s) && strings.IndexByte(" .-+*#0123456789", s[i]) >= 0 {
				if s[i] == '*' {
					types = append(types, 'd')
				}
				i++
			}
			if i >= len(s) {
				return "", nil, errors.New("expected type specifier after %")
			}
			var t byte
			switch s[i] {
			case 's':
				t = 's'
			case 'd':
				t = 'd'
			case 'o', 'x', 'X':
				t = 'u'
			case 'i':
				t = 'd'
				out[i] = 'd'
			case 'f', 'e', 'E', 'g', 'G':
				t = 'f'
			case 'a', 'A', 'F':
				// Go's fmt package does not implement C/POSIX F and its
				// x/X floating-point exponent and zero-padding differ from
				// a/A. Preserve the verb and route it through awkFloat's
				// fmt.Formatter implementation below. fmt still resolves
				// flags and dynamic width/precision before calling it.
				t = s[i]
			case 'u':
				t = 'u'
				out[i] = 'd'
			case 'c':
				t = 'c'
				out[i] = 's'
			default:
				return "", nil, fmt.Errorf("invalid format type %q", s[i])
			}
			types = append(types, t)
		}
	}

	// Dumb, non-LRU cache: just cache the first N formats
	format = string(out)
	if len(p.formatCache) < maxCachedFormats {
		p.formatCache[s] = cachedFormat{format, types}
	}
	return format, types, nil
}

// Guts of sprintf() function (also used by "printf" statement)
func (p *interp) sprintf(format string, args []value) (string, error) {
	format, args = normalizeNegativeDynamicPrecisions(format, args)
	format, args = normalizeOctalHashZeroPrecision(format, args)
	format, types, err := p.parseFmtTypes(format)
	if err != nil {
		return "", newError("format error: %s", err)
	}
	if len(types) > len(args) {
		return "", newError("format error: got %d args, expected %d", len(args), len(types))
	}
	converted := make([]any, 0, 7) // up to 7 args won't require heap allocation
	for i, t := range types {
		a := args[i]
		var v any
		switch t {
		case 's':
			v = p.toString(a)
		case 'd':
			v = int64(a.num())
		case 'f':
			v = a.num()
		case 'a', 'A', 'F':
			v = awkFloat(a.num())
		case 'u':
			v = uint64(int64(a.num()))
		case 'c':
			var c []byte
			n, isStr := a.isTrueStr()
			if isStr {
				s := p.toString(a)
				if len(s) == 0 {
					c = []byte{0}
				} else if p.chars {
					_, size := utf8.DecodeRuneInString(s)
					c = []byte(s[:size])
				} else {
					c = []byte{s[0]}
				}
			} else {
				if p.chars {
					buf := make([]byte, utf8.UTFMax)
					size := utf8.EncodeRune(buf, rune(n))
					c = buf[:size]
				} else {
					c = []byte{byte(n)}
				}
			}
			v = c
		}
		converted = append(converted, v)
	}
	return fmt.Sprintf(format, converted...), nil
}

// awkFloat implements the C/POSIX floating-point conversions that Go's fmt
// package does not provide directly. Keeping it as a fmt.Formatter lets fmt
// continue to parse flags and resolve * width/precision arguments for us.
type awkFloat float64

func (v awkFloat) Format(state fmt.State, verb rune) {
	n := float64(v)
	upper := verb == 'A' || verb == 'F'
	special := math.IsNaN(n) || math.IsInf(n, 0)

	sign := ""
	if !math.IsNaN(n) {
		switch {
		case math.Signbit(n):
			sign = "-"
			n = -n
		case state.Flag('+'):
			sign = "+"
		case state.Flag(' '):
			sign = " "
		}
	}

	precision, hasPrecision := state.Precision()
	var body string
	switch {
	case math.IsNaN(n):
		body = "nan"
	case math.IsInf(n, 1):
		body = "inf"
	case verb == 'F':
		if !hasPrecision {
			precision = 6
		}
		body = strconv.FormatFloat(n, 'f', precision, 64)
		if state.Flag('#') && precision == 0 {
			body += "."
		}
	default:
		body = formatHexFloat(n, precision, hasPrecision)
		body = trimHexExponentZeros(body)
		if state.Flag('#') {
			if exponent := strings.IndexByte(body, 'p'); exponent >= 0 &&
				!strings.Contains(body[:exponent], ".") {
				body = body[:exponent] + "." + body[exponent:]
			}
		}
	}
	if upper {
		body = strings.ToUpper(body)
	}

	width, hasWidth := state.Width()
	padding := 0
	if hasWidth && width > len(sign)+len(body) {
		padding = width - len(sign) - len(body)
	}
	if state.Flag('-') {
		_, _ = io.WriteString(state, sign)
		_, _ = io.WriteString(state, body)
		writePadding(state, ' ', padding)
		return
	}
	if state.Flag('0') && !special {
		_, _ = io.WriteString(state, sign)
		if (verb == 'a' || verb == 'A') && len(body) >= 2 &&
			(body[:2] == "0x" || body[:2] == "0X") {
			_, _ = io.WriteString(state, body[:2])
			writePadding(state, '0', padding)
			_, _ = io.WriteString(state, body[2:])
			return
		}
		writePadding(state, '0', padding)
		_, _ = io.WriteString(state, body)
		return
	}
	writePadding(state, ' ', padding)
	_, _ = io.WriteString(state, sign)
	_, _ = io.WriteString(state, body)
}

func formatHexFloat(n float64, precision int, hasPrecision bool) string {
	if !hasPrecision {
		precision = -1
	}
	body := strconv.FormatFloat(n, 'x', precision, 64)
	if !hasPrecision || n == 0 {
		return body
	}

	// FormatFloat renormalizes a significand that rounds from 1.xxx to 2.000
	// by incrementing the exponent. C printf and gawk retain the exponent of
	// the unrounded value and emit a leading 2 instead.
	_, exponent := math.Frexp(n)
	wantExponent := exponent - 1
	p := strings.IndexByte(body, 'p')
	if p < 0 || !strings.HasPrefix(body, "0x1") {
		return body
	}
	gotExponent, err := strconv.Atoi(body[p+1:])
	if err != nil || gotExponent != wantExponent+1 {
		return body
	}
	body = body[:2] + "2" + body[3:p] + "p"
	if wantExponent >= 0 {
		body += "+"
	}
	return body + strconv.Itoa(wantExponent)
}

func trimHexExponentZeros(s string) string {
	exponent := strings.IndexByte(s, 'p')
	if exponent < 0 || exponent+2 >= len(s) {
		return s
	}
	digits := exponent + 2 // skip p and its sign
	for digits+1 < len(s) && s[digits] == '0' {
		digits++
	}
	return s[:exponent+2] + s[digits:]
}

func writePadding(w io.Writer, b byte, count int) {
	if count > 0 {
		_, _ = io.WriteString(w, strings.Repeat(string(b), count))
	}
}

func substrChars(s string, pos int) string {
	// Count characters till we get to pos.
	chars := 1
	start := 0
	for start = range s {
		chars++
		if chars > pos {
			break
		}
	}
	if pos >= chars {
		start = len(s)
	}
	return s[start:]
}

func substrLengthChars(s string, pos, length int) string {
	// Count characters till we get to pos.
	chars := 1
	start := 0
	for start = range s {
		chars++
		if chars > pos {
			break
		}
	}
	if pos >= chars {
		start = len(s)
	}

	// Count characters from start till we reach length.
	chars = 0
	end := 0
	for end = range s[start:] {
		chars++
		if chars > length {
			break
		}
	}
	if length >= chars {
		end = len(s)
	} else {
		end += start
	}

	return s[start:end]
}
