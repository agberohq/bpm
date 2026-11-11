package key

import (
	"math"
	"sort"
)

// Confidence labels used by Assessment.
const (
	ConfidenceStrong    = "strong"
	ConfidenceModerate  = "moderate"
	ConfidenceAmbiguous = "ambiguous"
	ConfidenceNone      = "none"
)

// Candidate is one key and its correlation with the analysed chroma.
type Candidate struct {
	// Key is the display name, for example "C# minor".
	Key string `json:"key"`
	// Tonic is the pitch-class name of the key root.
	Tonic string `json:"tonic"`
	// Mode is "major" or "minor".
	Mode Mode `json:"mode"`
	// Correlation is the Pearson correlation with the key profile, in [-1, 1].
	Correlation float64 `json:"correlation"`
	// Rank is 1 for the best-scoring key.
	Rank int `json:"rank"`
}

// Assessment is the outcome of a Krumhansl-Schmuckler analysis.
//
// Domain is empty when no key reached the confidence threshold; callers must
// not assume a key was found.
type Assessment struct {
	Domain     string      `json:"domain"`
	Confidence string      `json:"confidence"`
	Margin     float64     `json:"margin"`
	Present    []Candidate `json:"present"`
	// All holds every one of the 24 keys when Options.IncludeAll is set.
	All []Candidate `json:"all,omitempty"`
}

// Options configures a Correlator.
type Options struct {
	// MinCorrelation is the absolute correlation a key needs to be reported.
	MinCorrelation float64
	// KeepRatio keeps candidates whose correlation is at least this fraction
	// of the best one. The reference implementation uses 0.9.
	KeepRatio float64
	// MaxCandidates caps Present.
	MaxCandidates int
	// StrongMargin and ModerateMargin classify the gap between the best and
	// the second-best correlation.
	StrongMargin   float64
	ModerateMargin float64
	// MinFrames is the number of chroma frames required before a key is
	// reported. One frame is 2048 samples (~46 ms) with the default extractor,
	// so the default of 16 frames requires roughly 0.75 s of audio. Musical
	// key estimates are only meaningful with several seconds of audio.
	MinFrames int
	// IncludeAll fills Assessment.All with all 24 candidates.
	IncludeAll bool
}

// DefaultOptions returns the default correlator configuration.
func DefaultOptions() Options {
	return Options{
		MinCorrelation: 0.5,
		KeepRatio:      0.9,
		MaxCandidates:  4,
		StrongMargin:   0.10,
		ModerateMargin: 0.03,
		MinFrames:      16,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.MinCorrelation == 0 {
		o.MinCorrelation = d.MinCorrelation
	}
	if o.KeepRatio == 0 {
		o.KeepRatio = d.KeepRatio
	}
	if o.MaxCandidates <= 0 {
		o.MaxCandidates = d.MaxCandidates
	}
	if o.StrongMargin == 0 {
		o.StrongMargin = d.StrongMargin
	}
	if o.ModerateMargin == 0 {
		o.ModerateMargin = d.ModerateMargin
	}
	if o.MinFrames <= 0 {
		o.MinFrames = d.MinFrames
	}
	return o
}

// Correlator scores chroma vectors against the 24 major and minor keys.
type Correlator struct {
	opts Options
}

// NewCorrelator returns a Correlator with the given options. Zero-valued
// fields fall back to DefaultOptions.
func NewCorrelator(opts Options) *Correlator {
	return &Correlator{opts: opts.withDefaults()}
}

// Options returns the effective options.
func (c *Correlator) Options() Options { return c.opts }

// Analyze scores chroma and decides which candidates are worth reporting.
//
// frames is the number of chroma frames the vector was built from; it guards
// against reporting a key for a fraction of a second of audio.
func (c *Correlator) Analyze(chroma Chroma, frames int) Assessment {
	all := Correlate(chroma)
	a := Assessment{Confidence: ConfidenceNone}
	if c.opts.IncludeAll {
		a.All = all
	}
	if frames < c.opts.MinFrames || chroma.Sum() <= 0 {
		return a
	}

	top := all[0]
	if top.Correlation < c.opts.MinCorrelation {
		return a
	}

	a.Domain = top.Key
	if len(all) > 1 {
		a.Margin = top.Correlation - all[1].Correlation
	}
	switch {
	case a.Margin >= c.opts.StrongMargin:
		a.Confidence = ConfidenceStrong
	case a.Margin >= c.opts.ModerateMargin:
		a.Confidence = ConfidenceModerate
	default:
		a.Confidence = ConfidenceAmbiguous
	}

	threshold := top.Correlation * c.opts.KeepRatio
	for _, cand := range all {
		if len(a.Present) >= c.opts.MaxCandidates {
			break
		}
		if cand.Correlation < c.opts.MinCorrelation || cand.Correlation < threshold {
			break
		}
		a.Present = append(a.Present, cand)
	}
	return a
}

// Correlate scores chroma against all 24 keys, sorted by decreasing
// correlation. Ties are broken deterministically by key name.
func Correlate(chroma Chroma) []Candidate {
	out := make([]Candidate, 0, 24)
	for tonic := 0; tonic < 12; tonic++ {
		rotated := Rotate(chroma, tonic)
		out = append(out,
			Candidate{
				Key:         Name(tonic, Major),
				Tonic:       tonicDisplay(tonic, Major),
				Mode:        Major,
				Correlation: Pearson(MajorProfile, rotated),
			},
			Candidate{
				Key:         Name(tonic, Minor),
				Tonic:       tonicDisplay(tonic, Minor),
				Mode:        Minor,
				Correlation: Pearson(MinorProfile, rotated),
			},
		)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Correlation != out[j].Correlation {
			return out[i].Correlation > out[j].Correlation
		}
		return out[i].Key < out[j].Key
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// Rotate returns chroma re-indexed so that element 0 is the tonic pitch class.
// This is equivalent to rotating the key profile to the tonic.
func Rotate(chroma Chroma, tonic int) [12]float64 {
	var out [12]float64
	for i := 0; i < 12; i++ {
		out[i] = chroma[((tonic+i)%12+12)%12]
	}
	return out
}

// Pearson returns the Pearson correlation coefficient of x and y. It returns 0
// when either vector has no variance.
func Pearson(x, y [12]float64) float64 {
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= 12
	my /= 12

	var num, dx, dy float64
	for i := range x {
		a := x[i] - mx
		b := y[i] - my
		num += a * b
		dx += a * a
		dy += b * b
	}
	if dx <= 0 || dy <= 0 {
		return 0
	}
	return num / math.Sqrt(dx*dy)
}
