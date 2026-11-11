// Package song combines tempo, key and structural analysis into a single
// pass over a PCM stream.
//
// Tempo comes from the bpm package, key candidates from the
// Krumhansl-Schmuckler correlator in the key package, and section boundaries
// from the self-similarity segmenter in the segment package. Global key
// analysis is computed over the whole song; segment analysis is computed
// independently per section, so a modulation never contaminates the global
// estimate.
package song

import (
	"fmt"

	"github.com/agberohq/bpm"
	"github.com/agberohq/bpm/key"
	"github.com/agberohq/bpm/segment"
)

// Options configures Analyze.
type Options struct {
	// Rate is the sample rate of the PCM stream.
	Rate float64
	// MinBPM and MaxBPM bound the tempo search.
	MinBPM bpm.BPM
	MaxBPM bpm.BPM
	// TempoInterval is the number of audio samples per energy sample.
	TempoInterval bpm.Samples
	// ScanSteps and ScanSamples control the tempo scan resolution.
	ScanSteps   uint
	ScanSamples uint

	// FrameSize and HopSize configure the chroma front end.
	FrameSize int
	HopSize   int
	// Whiten enables spectral whitening in the chroma extractor.
	Whiten bool

	// Segment configures structural boundary detection.
	Segment segment.Options
	// Key configures the correlator.
	Key key.Options

	// MinModulationSeconds is the shortest section that can be reported as a
	// modulation. Brief correlation wobble therefore cannot masquerade as a
	// key change.
	MinModulationSeconds float64

	// Labels enables heuristic section labels (intro/verse/chorus/...).
	Labels bool
	// Labeller overrides the heuristic labeller, for example with a model.
	// A nil Labeller with Labels set uses segment.HeuristicLabeller.
	Labeller segment.Labeller
	// LabellerOptions tunes the heuristic labeller.
	LabellerOptions segment.HeuristicOptions

	// BlockSize is the PCM block size used when reading from a stream.
	BlockSize int
}

// DefaultOptions returns the default analysis configuration (44.1 kHz mono).
func DefaultOptions() Options {
	return Options{
		Rate:                 44100,
		MinBPM:               bpm.DefaultMinBPM,
		MaxBPM:               bpm.DefaultMaxBPM,
		TempoInterval:        bpm.DefaultInterval,
		ScanSteps:            bpm.DefaultScanSteps,
		ScanSamples:          bpm.DefaultScanSamples,
		FrameSize:            key.DefaultFrameSize,
		HopSize:              key.DefaultHopSize,
		Segment:              segment.DefaultOptions(),
		Key:                  key.DefaultOptions(),
		MinModulationSeconds: 10,
		Labels:               true,
		BlockSize:            bpm.DefaultBlockSize,
	}
}

// withDefaults fills zero-valued fields, leaving explicitly set values alone.
func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.MinBPM == 0 {
		o.MinBPM = d.MinBPM
	}
	if o.MaxBPM == 0 {
		o.MaxBPM = d.MaxBPM
	}
	if o.TempoInterval <= 0 {
		o.TempoInterval = d.TempoInterval
	}
	if o.ScanSteps == 0 {
		o.ScanSteps = d.ScanSteps
	}
	if o.ScanSamples == 0 {
		o.ScanSamples = d.ScanSamples
	}
	if o.FrameSize == 0 {
		o.FrameSize = d.FrameSize
	}
	if o.HopSize == 0 {
		o.HopSize = d.HopSize
	}
	if o.BlockSize <= 0 {
		o.BlockSize = d.BlockSize
	}
	if o.MinModulationSeconds <= 0 {
		o.MinModulationSeconds = d.MinModulationSeconds
	}
	return o
}

// Validate reports configuration errors. It does not modify the receiver;
// zero-valued optional fields are interpreted as their defaults.
func (o Options) Validate() error {
	r := o.withDefaults()

	if !(r.Rate > 0) {
		return fmt.Errorf("song: sample rate must be > 0, got %v", r.Rate)
	}
	if r.MinBPM >= r.MaxBPM {
		return fmt.Errorf("song: min tempo %v must be below max tempo %v", r.MinBPM, r.MaxBPM)
	}

	if _, err := bpm.NewChecked(
		bpm.WithRate(bpm.Rate(r.Rate)),
		bpm.WithInterval(r.TempoInterval),
		bpm.WithTempoRange(r.MinBPM, r.MaxBPM),
		bpm.WithScanSteps(r.ScanSteps),
		bpm.WithScanSamples(r.ScanSamples),
	); err != nil {
		return err
	}

	ex := key.Extractor{
		Rate:      r.Rate,
		FrameSize: r.FrameSize,
		HopSize:   r.HopSize,
		MinHz:     key.DefaultMinHz,
		MaxHz:     key.DefaultMaxHz,
	}
	if err := ex.Validate(); err != nil {
		return err
	}

	return r.Segment.WithDefaults().Validate()
}
