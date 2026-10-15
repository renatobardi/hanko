package service

import (
	"math"
	"testing"
)

func TestI32Satura(t *testing.T) {
	for in, want := range map[int]int32{0: 0, 42: 42, -7: -7, math.MaxInt32 + 1: math.MaxInt32, math.MinInt32 - 1: math.MinInt32} {
		if got := i32(in); got != want {
			t.Errorf("i32(%d) = %d, quer %d", in, got, want)
		}
	}
}
