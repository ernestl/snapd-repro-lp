package main

import (
	"strings"
	"testing"
)

func TestColorHelpers(t *testing.T) {
	tests := []struct {
		name   string
		fn     func(string) string
		prefix string
	}{
		{"bold", bold, colorBold},
		{"dim", dim, colorDim},
		{"green", green, colorGreen},
		{"blue", blue, colorBlue},
		{"red", red, colorRed},
		{"yellow", yellow, colorYellow},
		{"cyan", cyan, colorCyan},
		{"boldCyan", boldCyan, colorBold + colorCyan},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.fn("hello")
			if !strings.HasPrefix(result, tt.prefix) {
				t.Errorf("%s: expected prefix %q, got %q", tt.name, tt.prefix, result)
			}
			if !strings.HasSuffix(result, colorReset) {
				t.Errorf("%s: expected suffix %q, got %q", tt.name, colorReset, result)
			}
			if !strings.Contains(result, "hello") {
				t.Errorf("%s: expected to contain 'hello', got %q", tt.name, result)
			}
		})
	}
}

func TestColorEmptyString(t *testing.T) {
	result := bold("")
	if result != colorBold+colorReset {
		t.Errorf("bold empty: got %q", result)
	}
}
