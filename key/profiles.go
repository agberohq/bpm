// Package key estimates musical keys from audio using the
// Krumhansl-Schmuckler key-finding algorithm.
//
// The algorithm correlates the pitch-class content of a passage with the
// empirical major and minor key profiles published by Krumhansl and Kessler
// (1982), rotating the profiles to all twelve tonics. All 24 correlations are
// preserved so callers can distinguish a decisive result from an ambiguous
// one.
//
// The chroma front end is an STFT/log-frequency implementation: frames are
// windowed, transformed with a radix-2 FFT, and spectral energy is folded into
// twelve pitch classes. It approximates librosa's chroma_cqt, which the
// reference implementation uses, without requiring a constant-Q transform.
package key

// Mode is the quality of a key.
type Mode string

const (
	// Major is the Ionian mode.
	Major Mode = "major"
	// Minor is the Aeolian mode, as used by the Krumhansl-Kessler profiles.
	Minor Mode = "minor"
)

// PitchClasses are the twelve chromatic pitch-class names starting at C, using
// sharps. They are the canonical lookup order; Name uses conventional
// enharmonic spelling for display.
var PitchClasses = [12]string{
	"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B",
}

// Conventional spellings: flat keys for the flat side of the circle of
// fifths, sharp keys for the sharp side. "D# major" is not a key anyone
// writes; "Eb major" is.
var (
	majorNames = [12]string{"C", "Db", "D", "Eb", "E", "F", "F#", "G", "Ab", "A", "Bb", "B"}
	minorNames = [12]string{"C", "C#", "D", "Eb", "E", "F", "F#", "G", "G#", "A", "Bb", "B"}
)

// Krumhansl-Kessler probe-tone profiles (Krumhansl & Kessler, 1982),
// indexed from the tonic.
var (
	MajorProfile = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
	MinorProfile = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}
)

// Profile returns the key profile for m. Unknown modes fall back to Major.
func Profile(m Mode) [12]float64 {
	if m == Minor {
		return MinorProfile
	}
	return MajorProfile
}

// TonicName returns the canonical (sharp) pitch-class name for a tonic,
// wrapping out-of-range values into [0, 11].
func TonicName(tonic int) string {
	return PitchClasses[((tonic%12)+12)%12]
}

// tonicDisplay returns the conventional spelling for a tonic in a mode.
func tonicDisplay(tonic int, m Mode) string {
	names := majorNames
	if m == Minor {
		names = minorNames
	}
	return names[((tonic%12)+12)%12]
}

// Name formats a key such as "C# minor" or "Eb major".
func Name(tonic int, m Mode) string {
	return tonicDisplay(tonic, m) + " " + string(m)
}

// Chroma is the relative intensity of the twelve pitch classes, starting at C.
type Chroma [12]float64

// Add returns c with o added pitch class by pitch class.
func (c Chroma) Add(o Chroma) Chroma {
	for i := range c {
		c[i] += o[i]
	}
	return c
}

// Scale returns c multiplied by f.
func (c Chroma) Scale(f float64) Chroma {
	for i := range c {
		c[i] *= f
	}
	return c
}

// Sum returns the total intensity.
func (c Chroma) Sum() float64 {
	var s float64
	for _, v := range c {
		s += v
	}
	return s
}

// Normalize divides the chroma by its maximum, so the loudest pitch class is
// 1. A silent chroma is returned unchanged.
func (c Chroma) Normalize() Chroma {
	max := 0.0
	for _, v := range c {
		if v > max {
			max = v
		}
	}
	if max <= 0 {
		return c
	}
	return c.Scale(1 / max)
}
