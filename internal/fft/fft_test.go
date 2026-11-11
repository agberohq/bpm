package fft

import (
	"math"
	"math/cmplx"
	"testing"
)

func naiveDFT(x []complex128) []complex128 {
	n := len(x)
	out := make([]complex128, n)
	for k := 0; k < n; k++ {
		var sum complex128
		for t := 0; t < n; t++ {
			angle := -2 * math.Pi * float64(k*t) / float64(n)
			sum += x[t] * complex(math.Cos(angle), math.Sin(angle))
		}
		out[k] = sum
	}
	return out
}

func TestNewPlanRejectsInvalidSize(t *testing.T) {
	for _, n := range []int{-8, 0, 3, 6, 100} {
		if _, err := NewPlan(n); err == nil {
			t.Fatalf("NewPlan(%d) succeeded, want error", n)
		}
	}
}

func TestTransformMatchesNaiveDFT(t *testing.T) {
	for _, n := range []int{1, 2, 4, 8, 16, 32} {
		p, err := NewPlan(n)
		if err != nil {
			t.Fatalf("NewPlan(%d): %v", n, err)
		}

		x := make([]complex128, n)
		for i := range x {
			x[i] = complex(math.Sin(float64(i)*0.7)+0.2*float64(i), math.Cos(float64(i)*0.3))
		}

		want := naiveDFT(x)
		got := make([]complex128, n)
		copy(got, x)
		p.Transform(got)

		for i := range want {
			if cmplx.Abs(got[i]-want[i]) > 1e-9 {
				t.Fatalf("n=%d bin %d: got %v want %v", n, i, got[i], want[i])
			}
		}
	}
}

func TestTransformSinePeaksAtExpectedBin(t *testing.T) {
	const (
		n    = 64
		bin  = 7
		rate = 64.0
	)
	p, err := NewPlan(n)
	if err != nil {
		t.Fatal(err)
	}

	x := make([]complex128, n)
	for i := range x {
		x[i] = complex(math.Sin(2*math.Pi*bin*float64(i)/n), 0)
	}
	p.Transform(x)

	best, bestMag := 0, 0.0
	for k := 1; k < n/2; k++ {
		if m := cmplx.Abs(x[k]); m > bestMag {
			best, bestMag = k, m
		}
	}
	if best != bin {
		t.Fatalf("peak bin = %d, want %d", best, bin)
	}
	if want := float64(n) / 2; math.Abs(bestMag-want) > 1e-9 {
		t.Fatalf("peak magnitude = %v, want %v", bestMag, want)
	}
}

func TestPowerSpectrumImpulseIsFlat(t *testing.T) {
	const n = 32
	p, err := NewPlan(n)
	if err != nil {
		t.Fatal(err)
	}

	frame := make([]float64, n)
	frame[0] = 1
	dst := make([]complex128, n/2+1)
	work := make([]complex128, n)
	p.PowerSpectrum(frame, dst, work)

	for k := range dst {
		if math.Abs(real(dst[k])-1) > 1e-9 {
			t.Fatalf("bin %d = %v, want 1", k, dst[k])
		}
	}
}

func TestPowerSpectrumBufferLengthPanics(t *testing.T) {
	p, err := NewPlan(8)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for mismatched buffers")
		}
	}()
	p.PowerSpectrum(make([]float64, 4), make([]complex128, 5), make([]complex128, 8))
}
