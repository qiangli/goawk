package interp_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/benhoyt/goawk/interp"
	"github.com/benhoyt/goawk/parser"
)

func execAWK(t *testing.T, source string, input string) (string, error) {
	t.Helper()
	prog, err := parser.ParseProgram([]byte(source), nil)
	if err != nil {
		return "", err
	}
	out := &bytes.Buffer{}
	in := strings.NewReader(input)
	config := &interp.Config{
		Stdin:  in,
		Output: out,
	}
	_, err = interp.ExecProgram(prog, config)
	if err != nil {
		return "", err
	}
	return out.String(), nil
}

func TestIntervalExecution(t *testing.T) {
	// 1. 255 / 256
	// a{255} should succeed
	src255 := `BEGIN { print match("` + strings.Repeat("a", 255) + `", /a{255}/) }`
	out, err := execAWK(t, src255, "")
	if err != nil || strings.TrimSpace(out) != "1" {
		t.Fatalf("a{255} failed: out=%q, err=%v", out, err)
	}

	// a{256} should fail parsing/compilation
	src256 := `BEGIN { print match("a", /a{256}/) }`
	_, err = execAWK(t, src256, "")
	if err == nil {
		t.Fatalf("a{256} should have failed closed")
	}

	// 2. Huge decimal / overflow
	srcHuge := `BEGIN { print match("a", /a{999999999999999999999999999999}/) }`
	_, err = execAWK(t, srcHuge, "")
	if err == nil {
		t.Fatalf("huge decimal bound should have failed closed")
	}

	// 3. Adjacent quantifiers execution tests
	// a{0002}{0003} -> matches 6 'a's
	src1 := `BEGIN { print match("aaaaaa", /a{0002}{0003}/), RSTART, RLENGTH }`
	out, err = execAWK(t, src1, "")
	if err != nil || strings.TrimSpace(out) != "1 1 6" {
		t.Fatalf("a{0002}{0003} match failed: out=%q, err=%v", out, err)
	}

	src1Fail := `BEGIN { print match("aaaaa", /a{0002}{0003}/) }`
	out, err = execAWK(t, src1Fail, "")
	if err != nil || strings.TrimSpace(out) != "0" {
		t.Fatalf("a{0002}{0003} non-match failed: out=%q, err=%v", out, err)
	}

	// a{2}{3}{4,} -> matches at least 24 'a's
	src2 := `BEGIN { print match("` + strings.Repeat("a", 24) + `", /a{2}{3}{4,}/), RLENGTH }`
	out, err = execAWK(t, src2, "")
	if err != nil || strings.TrimSpace(out) != "1 24" {
		t.Fatalf("a{2}{3}{4,} match failed: out=%q, err=%v", out, err)
	}

	// a{2,}{3}{4} -> matches at least 24 'a's
	src3 := `BEGIN { print match("` + strings.Repeat("a", 24) + `", /a{2,}{3}{4}/), RLENGTH }`
	out, err = execAWK(t, src3, "")
	if err != nil || strings.TrimSpace(out) != "1 24" {
		t.Fatalf("a{2,}{3}{4} match failed: out=%q, err=%v", out, err)
	}

	// a{2,3}{4} -> matches 8 to 12 'a's (leftmost-longest = 12 on string of 12)
	src4 := `BEGIN { print match("aaaaaaaaaaaa", /a{2,3}{4}/), RLENGTH }`
	out, err = execAWK(t, src4, "")
	if err != nil || strings.TrimSpace(out) != "1 12" {
		t.Fatalf("a{2,3}{4} match failed: out=%q, err=%v", out, err)
	}

	// a{2}{3,4} -> matches 6 to 8 'a's (leftmost-longest = 8 on string of 8)
	src5 := `BEGIN { print match("aaaaaaaa", /a{2}{3,4}/), RLENGTH }`
	out, err = execAWK(t, src5, "")
	if err != nil || strings.TrimSpace(out) != "1 8" {
		t.Fatalf("a{2}{3,4} match failed: out=%q, err=%v", out, err)
	}

	// 4. Nested bracket atoms & preserved braces
	srcBracket := `BEGIN { print match("0", /[[:digit:]_{0002}]/), match("{", /[[:digit:]_{0002}]/) }`
	out, err = execAWK(t, srcBracket, "")
	if err != nil || strings.TrimSpace(out) != "1 1" {
		t.Fatalf("nested bracket atoms failed: out=%q, err=%v", out, err)
	}

	// 5. Escapes & Malformed syntax
	srcEscape := `BEGIN { print match("a{0002}", /a\{0002\}/) }`
	out, err = execAWK(t, srcEscape, "")
	if err != nil || strings.TrimSpace(out) != "1" {
		t.Fatalf("escapes failed: out=%q, err=%v", out, err)
	}

	srcMalformed := `BEGIN { print match("a", /a{5,2}/) }`
	_, err = execAWK(t, srcMalformed, "")
	if err == nil {
		t.Fatalf("a{5,2} should have failed closed")
	}

	// 6. FS, RS, split, sub, gsub
	// FS
	srcFS := `BEGIN { FS="a{0002}" } { print $1, $2 }`
	out, err = execAWK(t, srcFS, "helloaaworld")
	if err != nil || strings.TrimSpace(out) != "hello world" {
		t.Fatalf("FS failed: out=%q, err=%v", out, err)
	}

	// RS
	srcRS := `BEGIN { RS="b{0002}" } { print $0 }`
	out, err = execAWK(t, srcRS, "line1bbline2")
	if err != nil || strings.TrimSpace(out) != "line1\nline2" {
		t.Fatalf("RS failed: out=%q, err=%v", out, err)
	}

	// split
	srcSplit := `BEGIN { n = split("oneaatwoaathree", arr, "a{0002}"); print n, arr[1], arr[2], arr[3] }`
	out, err = execAWK(t, srcSplit, "")
	if err != nil || strings.TrimSpace(out) != "3 one two three" {
		t.Fatalf("split failed: out=%q, err=%v", out, err)
	}

	// sub
	srcSub := `BEGIN { s = "fooaabar"; sub("a{0002}", "Z", s); print s }`
	out, err = execAWK(t, srcSub, "")
	if err != nil || strings.TrimSpace(out) != "fooZbar" {
		t.Fatalf("sub failed: out=%q, err=%v", out, err)
	}

	// gsub
	srcGsub := `BEGIN { s = "fooaabaar"; gsub("a{0002}", "Z", s); print s }`
	out, err = execAWK(t, srcGsub, "")
	if err != nil || strings.TrimSpace(out) != "fooZbZr" {
		t.Fatalf("gsub failed: out=%q, err=%v", out, err)
	}

	// Fail closed tests for FS/RS/split/sub/gsub with oversized bounds (>255)
	srcFSBad := `BEGIN { FS="a{256}" }`
	_, err = execAWK(t, srcFSBad, "")
	if err == nil {
		t.Fatalf("FS=a{256} should have failed closed")
	}

	srcSplitBad := `BEGIN { split("x", arr, "a{256}") }`
	_, err = execAWK(t, srcSplitBad, "")
	if err == nil {
		t.Fatalf("split with a{256} should have failed closed")
	}

	srcSubBad := `BEGIN { s = "x"; sub("a{256}", "y", s) }`
	_, err = execAWK(t, srcSubBad, "")
	if err == nil {
		t.Fatalf("sub with a{256} should have failed closed")
	}

	srcGsubBad := `BEGIN { s = "x"; gsub("a{256}", "y", s) }`
	_, err = execAWK(t, srcGsubBad, "")
	if err == nil {
		t.Fatalf("gsub with a{256} should have failed closed")
	}
}
