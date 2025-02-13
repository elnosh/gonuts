package mint

import (
	"math"
	"testing"
)

func TestOverflowAddUint64(t *testing.T) {
	tests := []struct {
		a                uint64
		b                uint64
		expectedUint64   uint64
		expectedOverflow bool
	}{
		{
			a:                21,
			b:                42,
			expectedUint64:   63,
			expectedOverflow: false,
		},
		{
			a:                math.MaxUint64 - 5,
			b:                10,
			expectedUint64:   math.MaxUint64,
			expectedOverflow: true,
		},
	}

	for _, test := range tests {
		result, overflow := overflowAddUint64(test.a, test.b)
		if result != test.expectedUint64 {
			t.Fatalf("expected result '%v' but got '%v'", test.expectedUint64, result)
		}

		if overflow != test.expectedOverflow {
			t.Fatalf("expected overflow '%v' but got '%v'", test.expectedOverflow, overflow)
		}
	}
}

func TestUnderflowSubUint64(t *testing.T) {
	tests := []struct {
		a                 uint64
		b                 uint64
		expectedUint64    uint64
		expectedUnderflow bool
	}{
		{
			a:                 42,
			b:                 21,
			expectedUint64:    21,
			expectedUnderflow: false,
		},
		{
			a:                 10,
			b:                 210,
			expectedUint64:    0,
			expectedUnderflow: true,
		},
	}

	for _, test := range tests {
		result, underflow := underflowSubUint64(test.a, test.b)
		if result != test.expectedUint64 {
			t.Fatalf("expected result '%v' but got '%v'", test.expectedUint64, result)
		}

		if underflow != test.expectedUnderflow {
			t.Fatalf("expected overflow '%v' but got '%v'", test.expectedUnderflow, underflow)
		}
	}
}
