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
