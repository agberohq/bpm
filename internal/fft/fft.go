// Package fft implements a small, dependency-free radix-2 fast Fourier
// transform.
//
// It is intentionally minimal: the analysis packages only need forward
// transforms of fixed, power-of-two sized real frames.
package fft

import (
	"errors"
	"math"
)

// ErrSize is returned when a plan is requested for a length that is not a
// positive power of two.
var ErrSize = errors.New("fft: size must be a positive power of two")

// Plan holds the precomputed bit-reversal permutation and twiddle factors for
// a transform of a fixed size. A Plan is safe for concurrent use as long as
// each goroutine supplies its own work buffer.
type Plan struct {
	n   int
	rev []int
	cos []float64
	sin []float64
}

// NewPlan returns a Plan for transforms of length n, which must be a positive
// power of two.
func NewPlan(n int) (*Plan, error) {
	if n <= 0 || n&(n-1) != 0 {
		return nil, ErrSize
	}

	bits := 0
	for 1<<bits < n {
		bits++
	}

	p := &Plan{
		n:   n,
		rev: make([]int, n),
		cos: make([]float64, n/2),
		sin: make([]float64, n/2),
	}
	for i := 0; i < n; i++ {
		p.rev[i] = reverseBits(i, bits)
	}
	for i := 0; i < n/2; i++ {
		angle := -2 * math.Pi * float64(i) / float64(n)
		p.cos[i] = math.Cos(angle)
		p.sin[i] = math.Sin(angle)
	}
	return p, nil
}

// Size returns the transform length.
func (p *Plan) Size() int { return p.n }

// Transform computes the discrete Fourier transform of x in place.
//
// The length of x must equal the plan size; a mismatch panics, mirroring the
// behaviour of the standard library's zero-copy helpers.
func (p *Plan) Transform(x []complex128) {
	if len(x) != p.n {
		panic("fft: transform length does not match plan size")
	}

	for i, j := range p.rev {
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}

	for size := 2; size <= p.n; size <<= 1 {
		half := size >> 1
		stride := p.n / size
		for start := 0; start < p.n; start += size {
			for k := 0; k < half; k++ {
				w := complex(p.cos[k*stride], p.sin[k*stride])
				a := start + k
				b := a + half
				t := w * x[b]
				x[b] = x[a] - t
				x[a] += t
			}
		}
	}
}

// PowerSpectrum computes |X[k]|^2 for k in [0, n/2] from the real signal frame,
// writing the result into dst. Both frame and dst must have the expected
// lengths (frame: plan size, dst: plan size/2+1) and work must be a scratch
// buffer of the plan size.
func (p *Plan) PowerSpectrum(frame []float64, dst, work []complex128) {
	if len(frame) != p.n || len(work) != p.n || len(dst) != p.n/2+1 {
		panic("fft: power spectrum buffer length mismatch")
	}
	for i, v := range frame {
		work[i] = complex(v, 0)
	}
	p.Transform(work)
	for i := range dst {
		re := real(work[i])
		im := imag(work[i])
		dst[i] = complex(re*re+im*im, 0)
	}
}

func reverseBits(v, bits int) int {
	var r int
	for i := 0; i < bits; i++ {
		r = (r << 1) | (v & 1)
		v >>= 1
	}
	return r
}
