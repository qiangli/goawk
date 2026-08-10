package interp

import (
	"fmt"
	"math"
	"testing"
)

func TestAWKFloatFormat(t *testing.T) {
	tests := []struct {
		name   string
		format string
		value  float64
		want   string
	}{
		{"positive zero", "%a", 0, "0x0p+0"},
		{"negative zero", "%a", math.Copysign(0, -1), "-0x0p+0"},
		{"uppercase fraction", "%A", 0.125, "0X1P-3"},
		{"negative fraction", "%a", -0.125, "-0x1p-3"},
		{"alternate no precision", "%#.0a", 0.125, "0x1.p-3"},
		{"explicit precision", "%.2A", 0.125, "0X1.00P-3"},
		{"rounding carry", "%.2a", 1.999, "0x2.00p+0"},
		{"subnormal", "%a", math.SmallestNonzeroFloat64, "0x1p-1074"},
		{"zero padding after prefix", "%015.6a", 0.125, "0x001.000000p-3"},
		{"plus before prefix", "%+015.6a", 0.125, "+0x01.000000p-3"},
		{"space before prefix", "% 015.6a", 0.125, " 0x01.000000p-3"},
		{"left adjustment", "%-15.6a", 0.125, "0x1.000000p-3  "},
		{"uppercase fixed", "%F", 0.125, "0.125000"},
		{"fixed precision", "%.2F", 0.125, "0.12"},
		{"fixed alternate", "%#.0F", 1, "1."},
		{"fixed zero padding", "%010F", 0.125, "000.125000"},
		{"fixed negative zero", "%F", math.Copysign(0, -1), "-0.000000"},
		{"positive infinity", "%F", math.Inf(1), "INF"},
		{"negative infinity", "%F", math.Inf(-1), "-INF"},
		{"not a number", "%F", math.NaN(), "NAN"},
		{"special ignores zero padding", "%010a", math.Inf(1), "       inf"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := fmt.Sprintf(test.format, awkFloat(test.value)); got != test.want {
				t.Fatalf("Sprintf(%q, %v) = %q, want %q", test.format, test.value, got, test.want)
			}
		})
	}
}

func TestSprintfNegativeDynamicPrecisionIsOmitted(t *testing.T) {
	p := &interp{formatCache: make(map[string]cachedFormat)}
	tests := []struct {
		name   string
		format string
		args   []value
		want   string
	}{
		{"hex float", "%.*a", []value{num(-1), num(0.125)}, "0x1p-3"},
		{"fixed float", "%.*F", []value{num(-1), num(0.125)}, "0.125000"},
		{"existing float", "%.*f", []value{num(-1), num(0.125)}, "0.125000"},
		{"negative width and precision", "%*.*a", []value{num(-12), num(-1), num(0.125)}, "0x1p-3      "},
		{"multiple conversions", "%.*a %.1F", []value{num(-1), num(0.125), num(0.125)}, "0x1p-3 0.1"},
		{"fractional negative precision hex", "%.*a", []value{num(-0.5), num(0.1)}, "0x2p-4"},
		{"fractional negative precision uppercase fixed", "%.*F", []value{num(-0.5), num(0.125)}, "0"},
		{"fractional negative precision existing float", "%.*f", []value{num(-0.5), num(0.125)}, "0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := p.sprintf(test.format, test.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("sprintf(%q) = %q, want %q", test.format, got, test.want)
			}
		})
	}
}

// POSIX requires "%#o" to print a single "0" for a zero value even when the
// precision is zero, whereas Go's fmt yields an empty string. See
// normalizeOctalHashZeroPrecision.
func TestSprintfOctalHashZeroPrecision(t *testing.T) {
	p := &interp{formatCache: make(map[string]cachedFormat)}
	tests := []struct {
		name   string
		format string
		args   []value
		want   string
	}{
		{"static zero precision zero value", "%#.0o", []value{num(0)}, "0"},
		{"static zero precision nonzero value", "%#.0o", []value{num(8)}, "010"},
		{"dynamic zero precision zero value", "%#.*o", []value{num(0), num(0)}, "0"},
		{"dynamic zero precision nonzero value", "%#.*o", []value{num(0), num(8)}, "010"},
		{"positive precision zero value preserved", "%#.3o", []value{num(0)}, "000"},
		{"dynamic positive precision preserved", "%#.*o", []value{num(3), num(0)}, "000"},
		{"width right justified", "%#5.0o", []value{num(0)}, "    0"},
		{"width left justified", "%#-5.0o", []value{num(0)}, "0    "},
		{"zero flag suppressed by precision", "%#05.0o", []value{num(0)}, "    0"},
		{"plain zero precision stays empty", "%.0o", []value{num(0)}, ""},
		{"plain dynamic zero precision stays empty", "%.*o", []value{num(0), num(0)}, ""},
		{"no hash flag no change nonzero", "%.0o", []value{num(8)}, "10"},
		{"other verb unaffected", "%#.0x", []value{num(0)}, ""},
		{"multiple conversions", "%#.0o-%#.*o", []value{num(0), num(0), num(8)}, "0-010"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := p.sprintf(test.format, test.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("sprintf(%q, %v) = %q, want %q", test.format, test.args, got, test.want)
			}
		})
	}
}
