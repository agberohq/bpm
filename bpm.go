// Package bpm detects the tempo of mono float32 PCM audio.
//
// It also provides the streaming primitives used by the higher level
// analysis packages: block-wise PCM decoding, an incremental energy
// accumulator, and the autodifference tempo estimator.
//
// # Input
//
// All entry points consume raw, little-endian float32 PCM in the range
// [-1, 1] with no header, matching the output of most decoders once the
// container has been parsed. Downmix multi-channel audio to mono before
// analysis.
//
// # Zero values and invalid configuration
//
// A Processor built with New is validated lazily. Call Validate to check a
// configuration up front; every analysis method guards against invalid
// settings and returns an error or NaN rather than panicking or blocking.
package bpm

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// BPM is a tempo in beats per minute.
type BPM float64

// Rate is an audio sample rate in Hz.
type Rate float64

// Interval is a distance in energy-signal samples.
type Interval float64

// Samples is a count of audio samples.
type Samples int

const (
	DefaultRate     Rate    = 44100
	DefaultInterval Samples = 128
	DefaultMinBPM   BPM     = 84
	DefaultMaxBPM   BPM     = 146

	DefaultBlockSize   = 4096
	DefaultScanSteps   = 1024
	DefaultScanSamples = 1024

	// MaxScanProbes bounds Steps*Samples so a typo cannot turn a tempo scan
	// into an unbounded computation. The default configuration uses about
	// one million probes.
	MaxScanProbes = 1 << 26

	secondsPerMinute = 60
	attackDivisor    = 8
	releaseDivisor   = 512
)

// beatOffsets / noBeatOffsets are probe offsets in energy samples.
var beatOffsets = [...]float64{
	-32, -16, -8, -4, -2, -1,
	1, 2, 4, 8, 16, 32,
}

var noBeatOffsets = [...]float64{
	-0.5, -0.25, 0.25, 0.5,
}

var (
	// ErrTruncatedInput is returned when the PCM stream ends mid-sample.
	// Complete samples read before the truncation are still returned.
	ErrTruncatedInput = errors.New("bpm: truncated float32 sample")

	// ErrInvalidConfig is returned when a Processor is configured with
	// values that cannot produce a meaningful result.
	ErrInvalidConfig = errors.New("bpm: invalid configuration")

	// ErrNoResult is returned when the input is too short, silent, or
	// otherwise lacks the variation needed to estimate a tempo.
	ErrNoResult = errors.New("bpm: insufficient signal for tempo detection")
)

// ReadSamples decodes little-endian float32 PCM from r in blocks and calls fn
// for each decoded block until the stream ends, an error occurs, or fn returns
// an error.
//
// blockSize is measured in samples and defaults to DefaultBlockSize when it is
// not positive. The slice passed to fn is owned by the caller and must not be
// retained after fn returns.
//
// A trailing partial sample is reported as ErrTruncatedInput after every
// complete sample has been delivered.
func ReadSamples(r io.Reader, blockSize int, fn func([]float32) error) error {
	if r == nil {
		return errors.New("bpm: nil reader")
	}
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}

	buf := make([]byte, blockSize*4)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			whole := n - n%4
			if whole > 0 {
				block := make([]float32, whole/4)
				for i := range block {
					bits := binary.LittleEndian.Uint32(buf[i*4 : i*4+4])
					block[i] = math.Float32frombits(bits)
				}
				if ferr := fn(block); ferr != nil {
					return ferr
				}
			}
			if whole != n {
				return ErrTruncatedInput
			}
		}

		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return nil
		case err != nil:
			return err
		}
	}
}

// ReadFloat32 reads little-endian float32 PCM from r into a slice.
//
// A trailing partial sample is reported as ErrTruncatedInput; the complete
// samples read before it are still returned. A non-EOF read error is returned
// together with the samples decoded so far.
func ReadFloat32(r io.Reader) ([]float32, error) {
	out := make([]float32, 0)
	err := ReadSamples(r, DefaultBlockSize, func(block []float32) error {
		out = append(out, block...)
		return nil
	})
	return out, err
}

// stepEnergy applies one attack/release update to v given input z.
//
// Non-finite input is treated as silence so a single corrupt sample cannot
// invalidate an entire analysis.
func stepEnergy(v, z float32) float32 {
	if math.IsNaN(float64(z)) || math.IsInf(float64(z), 0) {
		z = 0
	}
	if z < 0 {
		z = -z
	}
	if z > v {
		return v + (z-v)/attackDivisor
	}
	return v - (v-z)/releaseDivisor
}
