package primitives

import (
	"slices"
	"testing"
)

func TestNormalizeArgs(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{nil, []string{}},
		{[]string{"-force"}, []string{"-force"}},
		{[]string{"--force"}, []string{"-force"}},
		// A value after = is part of the flag, not a second dash to collapse.
		{[]string{"--gate=covered:root"}, []string{"-gate=covered:root"}},
		// A bare operand is not a flag, and neither is the `--` separator's
		// meaning something this helper invents.
		{[]string{"covered", "--verdict", "-h"}, []string{"covered", "-verdict", "-h"}},
	}
	for _, c := range cases {
		got := NormalizeArgs(c.in)
		if !slices.Equal(got, c.want) {
			t.Errorf("NormalizeArgs(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
