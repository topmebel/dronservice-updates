//go:build linux

package v4l2

import "testing"

func TestFourCC(t *testing.T) {
	if got := fourCC(0x47504a4d); got != "MJPG" {
		t.Fatalf("fourCC() = %q, want MJPG", got)
	}
}

func TestFormatFPS(t *testing.T) {
	tests := []struct {
		numerator   uint32
		denominator uint32
		want        string
	}{
		{numerator: 1, denominator: 30, want: "30"},
		{numerator: 1001, denominator: 30000, want: "29.97"},
		{numerator: 0, denominator: 30, want: "unknown"},
	}

	for _, tt := range tests {
		if got := formatFPS(tt.numerator, tt.denominator); got != tt.want {
			t.Fatalf("formatFPS(%d, %d) = %q, want %q", tt.numerator, tt.denominator, got, tt.want)
		}
	}
}
