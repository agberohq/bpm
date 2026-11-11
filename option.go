package bpm

import (
	"io"
	"math/rand"
)

// Option configures a Processor.
type Option func(*Processor)

// WithRate sets the audio sample rate.
func WithRate(r Rate) Option { return func(p *Processor) { p.Rate = r } }

// WithInterval sets the number of audio samples per energy sample.
func WithInterval(n Samples) Option { return func(p *Processor) { p.Interval = n } }

// WithTempoRange sets the slowest and fastest tempos considered by Scan.
func WithTempoRange(min, max BPM) Option {
	return func(p *Processor) { p.MinBPM, p.MaxBPM = min, max }
}

// WithScanSteps sets how many candidate intervals Scan evaluates.
func WithScanSteps(n uint) Option { return func(p *Processor) { p.Steps = n } }

// WithScanSamples sets how many autodifference samples are averaged per
// candidate interval.
func WithScanSamples(n uint) Option { return func(p *Processor) { p.Samples = n } }

// WithRand sets the random source used by AutoDifference. Passing nil
// restores the deterministic default.
func WithRand(r *rand.Rand) Option {
	return func(p *Processor) {
		if r == nil {
			r = rand.New(rand.NewSource(1))
		}
		p.rand = r
	}
}

// WithEnergyTrace writes the energy signal as "time\tvalue" lines to w.
func WithEnergyTrace(w io.Writer) Option { return func(p *Processor) { p.energyTrace = w } }

// WithDiffTrace writes the autodifference scan as "bpm\tvalue" lines to w.
func WithDiffTrace(w io.Writer) Option { return func(p *Processor) { p.diffTrace = w } }
