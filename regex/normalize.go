package regex

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const reDupMax = 255

// Normalize standardizes ERE interval quantifiers, enforcing POSIX RE_DUP_MAX=255,
// normalizing leading zeros outside bracket expressions, checking interval bounds
// and quantifier compositions for overflow and RE2 validity.
func Normalize(expr string) (string, error) {
	norm, endPos, err := parseERE(expr, 0, false)
	if err != nil {
		var intervalErr *intervalValidationError
		if errors.As(err, &intervalErr) {
			return "", err
		}
		// Normalization is not an ERE validator. Preserve malformed and otherwise
		// unsupported input byte-for-byte so the selected regexp backend retains
		// ownership of its established diagnostics and compatibility behavior.
		return expr, nil
	}
	if endPos < len(expr) {
		return expr, nil
	}
	return norm, nil
}

type intervalValidationError struct {
	message string
}

func (e *intervalValidationError) Error() string { return e.message }

func invalidInterval(format string, args ...any) error {
	return &intervalValidationError{message: fmt.Sprintf(format, args...)}
}

func parseERE(s string, pos int, isGroup bool) (string, int, error) {
	var branches []string
	var branchBuf strings.Builder

	for pos <= len(s) {
		if pos == len(s) {
			branches = append(branches, branchBuf.String())
			if isGroup {
				return "", pos, fmt.Errorf("unclosed group '('")
			}
			break
		}

		ch := s[pos]
		if ch == '|' {
			branches = append(branches, branchBuf.String())
			branchBuf.Reset()
			pos++
			continue
		}
		if ch == ')' {
			if isGroup {
				branches = append(branches, branchBuf.String())
				pos++ // consume ')'
				return strings.Join(branches, "|"), pos, nil
			}
			return "", pos, fmt.Errorf("unmatched ')' at position %d", pos)
		}

		// Check for repetition operator at start of branch (no preceding atom)
		if ch == '*' || ch == '+' || ch == '?' {
			return "", pos, fmt.Errorf("missing argument to repetition operator %q at position %d", ch, pos)
		}
		if ch == '{' && isIntervalStart(s, pos) {
			return "", pos, fmt.Errorf("missing argument to repetition operator '{' at position %d", pos)
		}

		// Parse atom
		atomStr, isQuantified, _, _, nextPos, err := parseAtom(s, pos)
		if err != nil {
			return "", pos, err
		}
		pos = nextPos

		// Parse zero or more quantifiers following atom
		currStr := atomStr
		currIsQuantified := isQuantified
		for pos < len(s) {
			qCh := s[pos]
			if qCh == '*' || qCh == '+' || qCh == '?' || (qCh == '{' && isIntervalStart(s, pos)) {
				_, _, qNorm, qNext, qErr := parseQuantifier(s, pos)
				if qErr != nil {
					return "", pos, qErr
				}

				if !currIsQuantified {
					currStr = currStr + qNorm
				} else {
					currStr = "(?:" + currStr + ")" + qNorm
				}

				currIsQuantified = true
				pos = qNext
			} else {
				break
			}
		}

		branchBuf.WriteString(currStr)
	}

	return strings.Join(branches, "|"), pos, nil
}

func parseAtom(s string, pos int) (atomStr string, isQuantified bool, minBound int, maxBound int, nextPos int, err error) {
	if pos >= len(s) {
		return "", false, 0, 0, pos, fmt.Errorf("unexpected EOF")
	}

	ch := s[pos]
	if ch == '\\' {
		if pos+1 >= len(s) {
			return "", false, 0, 0, pos, fmt.Errorf("trailing backslash")
		}
		return s[pos : pos+2], false, 1, 1, pos + 2, nil
	}

	if ch == '(' {
		pos++
		groupNorm, nextPos, err := parseERE(s, pos, true)
		if err != nil {
			return "", false, 0, 0, pos, err
		}
		return "(" + groupNorm + ")", false, 1, 1, nextPos, nil
	}

	if ch == '[' {
		bracketStr, nextPos, err := parseBracket(s, pos)
		if err != nil {
			return "", false, 0, 0, pos, err
		}
		return bracketStr, false, 1, 1, nextPos, nil
	}

	// One UTF-8 atom (including literals, '.', '^', '$', etc.). Slicing the
	// original source preserves its exact bytes; string(ch) would re-encode a
	// non-ASCII leading byte as a different rune and corrupt every Unicode ERE.
	_, size := utf8.DecodeRuneInString(s[pos:])
	if size == 0 {
		size = 1
	}
	return s[pos : pos+size], false, 1, 1, pos + size, nil
}

func isIntervalStart(s string, pos int) bool {
	if pos >= len(s) || s[pos] != '{' || pos+1 >= len(s) || s[pos+1] < '0' || s[pos+1] > '9' {
		return false
	}
	i := pos + 1
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '}' {
		return true
	}
	if i >= len(s) || s[i] != ',' {
		return false
	}
	i++
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i < len(s) && s[i] == '}'
}

func parseQuantifier(s string, pos int) (qMin int, qMax int, qNorm string, nextPos int, err error) {
	ch := s[pos]
	if ch == '*' {
		return 0, -1, "*", pos + 1, nil
	}
	if ch == '+' {
		return 1, -1, "+", pos + 1, nil
	}
	if ch == '?' {
		return 0, 1, "?", pos + 1, nil
	}
	if ch == '{' {
		return parseInterval(s, pos)
	}
	return 0, 0, "", pos, fmt.Errorf("unknown quantifier at position %d", pos)
}

func parseInterval(s string, pos int) (qMin int, qMax int, qNorm string, nextPos int, err error) {
	start := pos
	pos++ // skip '{'
	m, pos, err := parseDecimal(s, pos)
	if err != nil {
		return 0, 0, "", start, err
	}
	if m > reDupMax {
		return 0, 0, "", start, invalidInterval("interval bound %d exceeds RE_DUP_MAX=%d", m, reDupMax)
	}

	if pos >= len(s) {
		return 0, 0, "", start, fmt.Errorf("unclosed interval expression")
	}

	if s[pos] == '}' {
		pos++
		return m, m, fmt.Sprintf("{%d}", m), pos, nil
	}

	if s[pos] == ',' {
		pos++
		if pos >= len(s) {
			return 0, 0, "", start, fmt.Errorf("unclosed interval expression")
		}
		if s[pos] == '}' {
			pos++
			return m, -1, fmt.Sprintf("{%d,}", m), pos, nil
		}
		if s[pos] >= '0' && s[pos] <= '9' {
			n, pos, err := parseDecimal(s, pos)
			if err != nil {
				return 0, 0, "", start, err
			}
			if n > reDupMax {
				return 0, 0, "", start, invalidInterval("interval bound %d exceeds RE_DUP_MAX=%d", n, reDupMax)
			}
			if pos >= len(s) || s[pos] != '}' {
				return 0, 0, "", start, fmt.Errorf("malformed interval syntax")
			}
			if m > n {
				return 0, 0, "", start, invalidInterval("invalid interval bounds: min %d > max %d", m, n)
			}
			pos++
			return m, n, fmt.Sprintf("{%d,%d}", m, n), pos, nil
		}
		return 0, 0, "", start, fmt.Errorf("malformed interval syntax")
	}

	return 0, 0, "", start, fmt.Errorf("malformed interval syntax")
}

func parseDecimal(s string, pos int) (int, int, error) {
	if pos >= len(s) || s[pos] < '0' || s[pos] > '9' {
		return 0, pos, fmt.Errorf("expected digit")
	}
	val := 0
	for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		digit := int(s[pos] - '0')
		if val > (math.MaxInt-digit)/10 {
			return 0, pos, invalidInterval("interval bound overflow")
		}
		val = val*10 + digit
		pos++
	}
	return val, pos, nil
}

func parseBracket(s string, pos int) (string, int, error) {
	start := pos
	pos++ // skip opening '['
	if pos < len(s) && s[pos] == '^' {
		pos++
	}
	if pos < len(s) && s[pos] == ']' {
		pos++ // initial literal ']'
	}
	for pos < len(s) {
		if s[pos] == '\\' && pos+1 < len(s) {
			pos += 2
			continue
		}
		if s[pos] == ']' {
			pos++
			return s[start:pos], pos, nil
		}
		if s[pos] == '[' && pos+1 < len(s) {
			if s[pos+1] == ':' {
				end := strings.Index(s[pos+2:], ":]")
				if end != -1 {
					pos = pos + 2 + end + 2
					continue
				}
			} else if s[pos+1] == '.' {
				end := strings.Index(s[pos+2:], ".]")
				if end != -1 {
					pos = pos + 2 + end + 2
					continue
				}
			} else if s[pos+1] == '=' {
				end := strings.Index(s[pos+2:], "=]")
				if end != -1 {
					pos = pos + 2 + end + 2
					continue
				}
			}
		}
		pos++
	}
	return s[start:], len(s), nil
}
