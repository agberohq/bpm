package bpm

import (
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
)

// Processor detects the tempo of mono float32 PCM audio.
//
// Construct with New. A Processor is not safe for concurrent use:
// AutoDifference advances its random source on every call. Create one
// Processor per goroutine, or protect it with a mutex.
type Processor struct {
	// Rate is the sample rate of the PCM being analysed.
	Rate Rate
	// Interval is the number of audio samples per energy sample.
	Interval Samples
	// MinBPM and MaxBPM bound the tempo search.
	MinBPM BPM
	MaxBPM BPM
	// Steps is the number of candidate intervals scanned between MinBPM and
	// MaxBPM.
	Steps uint
	// Samples is the number of autodifference probes averaged per candidate
	// interval.
	Samples uint

	rand        *rand.Rand
	energyTrace io.Writer
	diffTrace   io.Writer
}

// TempoEstimate is the result of a tempo scan.
type TempoEstimate struct {
	// BPM is the detected tempo. It is meaningful only when Valid is true.
	BPM BPM `json:"bpm"`
	// Confidence is in [0, 1]: how much the winning interval stands out
	// against the average candidate. Values below ~0.1 mean the objective
	// function is nearly flat and the result should be treated with care.
	Confidence float64 `json:"confidence"`
	// AtRangeEdge reports that the winner sat on the MinBPM/MaxBPM boundary,
	// which usually means the true tempo (or its half/double) lies outside
	// the configured range.
	AtRangeEdge bool `json:"at_range_edge"`
	// Valid reports whether a tempo could be estimated at all.
	Valid bool `json:"valid"`
}

// New returns a Processor configured with the given options.
func New(opts ...Option) *Processor {
	p := &Processor{
		Rate:     DefaultRate,
		Interval: DefaultInterval,
		MinBPM:   DefaultMinBPM,
		MaxBPM:   DefaultMaxBPM,
		Steps:    DefaultScanSteps,
		Samples:  DefaultScanSamples,
		rand:     rand.New(rand.NewSource(1)),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// NewChecked is New with eager validation, for callers that want configuration
// errors at construction time.
func NewChecked(opts ...Option) (*Processor, error) {
	p := New(opts...)
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// Validate reports whether the Processor can produce a useful result.
func (p *Processor) Validate() error {
	if err := p.streamConfigError(); err != nil {
		return err
	}
	switch {
	case !(p.MinBPM > 0):
		return fmt.Errorf("%w: min tempo must be > 0, got %v", ErrInvalidConfig, p.MinBPM)
	case !(p.MaxBPM > 0):
		return fmt.Errorf("%w: max tempo must be > 0, got %v", ErrInvalidConfig, p.MaxBPM)
	case p.MinBPM >= p.MaxBPM:
		return fmt.Errorf("%w: min tempo %v must be below max tempo %v", ErrInvalidConfig, p.MinBPM, p.MaxBPM)
	case p.Steps == 0:
		return fmt.Errorf("%w: scan steps must be > 0", ErrInvalidConfig)
	case p.Samples == 0:
		return fmt.Errorf("%w: scan samples must be > 0", ErrInvalidConfig)
	case p.rand == nil:
		return fmt.Errorf("%w: nil random source", ErrInvalidConfig)
	}

	if probes := uint64(p.Steps) * uint64(p.Samples); probes > MaxScanProbes {
		return fmt.Errorf("%w: steps*samples = %d exceeds the %d probe limit", ErrInvalidConfig, probes, uint64(MaxScanProbes))
	}
	if i := p.BPMToInterval(p.MaxBPM); i < 1 {
		return fmt.Errorf("%w: max tempo %v BPM is %v energy samples per beat; lower MaxBPM or raise Interval", ErrInvalidConfig, p.MaxBPM, i)
	}
	return nil
}

// streamConfigError validates only what is needed to turn PCM into an energy
// signal. Energy extraction does not scan for a tempo, so it must not fail
// because the scan budget is unusual.
func (p *Processor) streamConfigError() error {
	switch {
	case p == nil:
		return fmt.Errorf("%w: nil processor", ErrInvalidConfig)
	case !(p.Rate > 0):
		return fmt.Errorf("%w: rate must be > 0, got %v", ErrInvalidConfig, p.Rate)
	case !(p.Interval > 0):
		return fmt.Errorf("%w: interval must be > 0, got %v", ErrInvalidConfig, p.Interval)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Unit conversions
// ---------------------------------------------------------------------------

// BPMToInterval converts a tempo into an energy-space interval.
func (p *Processor) BPMToInterval(t BPM) Interval {
	if p == nil || !(p.Rate > 0) || !(p.Interval > 0) || !(t > 0) {
		return 0
	}
	beatsPerSecond := float64(t) / secondsPerMinute
	samplesPerBeat := float64(p.Rate) / beatsPerSecond
	return Interval(samplesPerBeat / float64(p.Interval))
}

// IntervalToBPM converts an energy-space interval into a tempo.
func (p *Processor) IntervalToBPM(i Interval) BPM {
	if p == nil || !(p.Rate > 0) || !(p.Interval > 0) || !(i > 0) {
		return 0
	}
	samplesPerBeat := float64(i) * float64(p.Interval)
	beatsPerSecond := float64(p.Rate) / samplesPerBeat
	return BPM(beatsPerSecond * secondsPerMinute)
}

// ---------------------------------------------------------------------------
// Energy signal
// ---------------------------------------------------------------------------

// EnergyAccumulator converts PCM into an energy signal incrementally, so long
// inputs can be analysed without retaining the PCM.
//
// An EnergyAccumulator is not safe for concurrent use. Its values are only
// meaningful when the owning Processor's configuration is valid.
type EnergyAccumulator struct {
	p      *Processor
	values []float32
	v      float32
	index  int
}

// NewEnergyAccumulator returns an accumulator for p.
func (p *Processor) NewEnergyAccumulator() *EnergyAccumulator {
	return &EnergyAccumulator{p: p}
}

// Write appends the energy samples produced by pcm.
func (a *EnergyAccumulator) Write(pcm []float32) {
	if a == nil || a.p == nil {
		return
	}
	interval := int(a.p.Interval)
	if interval <= 0 {
		return
	}
	for _, z := range pcm {
		a.v = stepEnergy(a.v, z)
		a.index++
		if a.index%interval != 0 {
			continue
		}
		a.p.traceEnergy(len(a.values), a.v)
		a.values = append(a.values, a.v)
	}
}

// Values returns the energy signal accumulated so far. The returned slice is
// owned by the accumulator and must not be modified.
func (a *EnergyAccumulator) Values() []float32 {
	if a == nil {
		return nil
	}
	return a.values
}

// Len returns the number of energy samples accumulated so far.
func (a *EnergyAccumulator) Len() int {
	if a == nil {
		return 0
	}
	return len(a.values)
}

// Energy converts PCM samples into a low-resolution energy signal sampled
// every Interval audio samples.
//
// It returns nil if the Processor configuration is invalid.
func (p *Processor) Energy(samples []float32) []float32 {
	if len(samples) == 0 || p.streamConfigError() != nil {
		return nil
	}
	acc := p.NewEnergyAccumulator()
	acc.values = make([]float32, 0, len(samples)/int(p.Interval)+1)
	acc.Write(samples)
	return acc.values
}

// EnergyReader is Energy for a stream; it does not retain the PCM.
//
// The energy samples decoded before an error are returned together with that
// error. A trailing partial sample is reported as ErrTruncatedInput.
func (p *Processor) EnergyReader(r io.Reader) ([]float32, error) {
	if err := p.streamConfigError(); err != nil {
		return nil, err
	}
	acc := p.NewEnergyAccumulator()
	err := ReadSamples(r, DefaultBlockSize, func(block []float32) error {
		acc.Write(block)
		return nil
	})
	return acc.values, err
}

func (p *Processor) traceEnergy(index int, v float32) {
	if p.energyTrace == nil {
		return
	}
	fmt.Fprintf(p.energyTrace, "%f\t%f\n",
		float64(index*int(p.Interval))/float64(p.Rate), v)
}

func (p *Processor) traceDiff(i Interval, diff float64) {
	if p.diffTrace == nil {
		return
	}
	fmt.Fprintf(p.diffTrace, "%f\t%f\n", p.IntervalToBPM(i), diff)
}

// ---------------------------------------------------------------------------
// Autodifference
// ---------------------------------------------------------------------------

// AutoDifference returns the weighted autodifference of the energy signal
// at the given interval. It advances the Processor's random source.
func (p *Processor) AutoDifference(nrg []float32, i Interval) float64 {
	if len(nrg) == 0 {
		return 0
	}
	mid := p.rand.Float64() * float64(len(nrg))
	v := p.Sample(nrg, mid)

	var diff, total float64
	for _, off := range beatOffsets {
		w := 1 / math.Abs(off)
		diff += w * math.Abs(p.Sample(nrg, mid+off*float64(i))-v)
		total += w
	}
	for _, off := range noBeatOffsets {
		w := math.Abs(off)
		diff -= w * math.Abs(p.Sample(nrg, mid+off*float64(i))-v)
		total += w
	}
	return diff / total
}

// Sample returns the energy value at offset (floored), or 0 if out of range.
func (p *Processor) Sample(nrg []float32, offset float64) float64 {
	n := math.Floor(offset)
	if math.IsNaN(n) || n < 0 || n >= float64(len(nrg)) {
		return 0
	}
	return float64(nrg[int(n)])
}

// ---------------------------------------------------------------------------
// Scan / Detect
// ---------------------------------------------------------------------------

// hasSignal reports whether nrg is long enough and varied enough for a tempo
// to be estimated. Silence and clips shorter than two beats are rejected: an
// estimator that returns MaxBPM for a silent file is worse than one that
// returns "no result".
func (p *Processor) hasSignal(nrg []float32) bool {
	if len(nrg) == 0 {
		return false
	}
	need := int(math.Ceil(2 * float64(p.BPMToInterval(p.MinBPM))))
	if need < 2 {
		need = 2
	}
	if len(nrg) < need {
		return false
	}

	var sum, sumsq float64
	for _, v := range nrg {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
		sum += f
		sumsq += f * f
	}
	n := float64(len(nrg))
	mean := sum / n
	if mean <= 0 {
		return false
	}
	variance := sumsq/n - mean*mean
	if variance <= 0 {
		return false
	}
	std := math.Sqrt(variance)
	// Require a coefficient of variation of at least 0.1%: an essentially
	// constant energy signal carries no rhythmic information.
	if std/mean < 1e-3 {
		return false
	}

	// Require several distinct onsets. A sustained pad with a single attack
	// has a varied but non-rhythmic energy curve, and estimating a tempo from
	// it would produce a confident, meaningless number.
	threshold := mean + 0.5*std
	onsets := 0
	above := false
	for _, v := range nrg {
		if float64(v) > threshold {
			if !above {
				onsets++
				above = true
			}
			continue
		}
		above = false
	}
	return onsets >= 4
}

// Estimate scans the configured tempo range and returns the best tempo along
// with a confidence measure.
//
// The returned estimate is invalid (Valid == false) when the configuration is
// unusable or the energy signal is too short or too flat.
func (p *Processor) Estimate(nrg []float32) TempoEstimate {
	if p.Validate() != nil || !p.hasSignal(nrg) {
		return TempoEstimate{}
	}

	minI := p.BPMToInterval(p.MinBPM) // slow tempo -> larger interval
	maxI := p.BPMToInterval(p.MaxBPM) // fast tempo -> smaller interval
	step := (minI - maxI) / Interval(p.Steps)
	if !(step > 0) {
		return TempoEstimate{}
	}

	best := math.Inf(1)
	var trough Interval
	var sum float64
	count := 0
	bestIndex := -1

	for i := maxI; i <= minI; i += step {
		var total float64
		for s := uint(0); s < p.Samples; s++ {
			total += p.AutoDifference(nrg, i)
		}
		avg := total / float64(p.Samples)
		p.traceDiff(i, avg)
		sum += avg
		if avg < best {
			best = avg
			trough = i
			bestIndex = count
		}
		count++
	}

	if count < 2 || bestIndex < 0 || math.IsNaN(float64(trough)) {
		return TempoEstimate{}
	}
	mean := sum / float64(count)
	if !(mean > 0) || math.IsInf(mean, 0) {
		return TempoEstimate{}
	}

	confidence := (mean - best) / mean
	return TempoEstimate{
		BPM:         p.IntervalToBPM(trough),
		Confidence:  clamp01(confidence),
		AtRangeEdge: bestIndex == 0 || bestIndex == count-1,
		Valid:       true,
	}
}

// Scan searches the configured tempo range and returns the tempo whose
// average autodifference is lowest, or NaN if the input or configuration
// is unusable.
func (p *Processor) Scan(nrg []float32) BPM {
	est := p.Estimate(nrg)
	if !est.Valid {
		return BPM(math.NaN())
	}
	return est.BPM
}

// Detect reads mono float32 PCM from r, builds the energy signal, and returns
// the detected tempo.
//
// A truncated final sample does not fail the analysis: the complete samples
// are used and the error is not reported. Detect returns ErrInvalidConfig for
// bad settings and ErrNoResult when the input is too short or too flat.
func (p *Processor) Detect(r io.Reader) (BPM, error) {
	est, err := p.DetectEstimate(r)
	if err != nil {
		return BPM(math.NaN()), err
	}
	if !est.Valid {
		return BPM(math.NaN()), ErrNoResult
	}
	return est.BPM, nil
}

// DetectEstimate is Detect with confidence information.
func (p *Processor) DetectEstimate(r io.Reader) (TempoEstimate, error) {
	nrg, err := p.EnergyReader(r)
	if err != nil && !errors.Is(err, ErrTruncatedInput) {
		return TempoEstimate{}, err
	}
	return p.Estimate(nrg), nil
}

// DetectSamples is Detect for an in-memory PCM buffer. It returns NaN when no
// tempo could be estimated.
func (p *Processor) DetectSamples(pcm []float32) BPM {
	return p.Scan(p.Energy(pcm))
}

func clamp01(v float64) float64 {
	switch {
	case math.IsNaN(v) || v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
