package song

import (
	"errors"
	"fmt"
	"io"

	"github.com/agberohq/bpm"
	"github.com/agberohq/bpm/key"
	"github.com/agberohq/bpm/segment"
)

// Analyze reads mono float32 PCM from r and returns the tempo, the global key
// and the per-section key analysis.
//
// The stream is consumed once: energy for the tempo estimator and chroma for
// the key analysis are accumulated in the same pass, so memory stays
// proportional to the number of chroma frames rather than to the audio length.
//
// A truncated final sample is tolerated; any other read error is returned.
func Analyze(r io.Reader, opts Options) (*Result, error) {
	eng, err := newEngine(opts)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("song: nil reader")
	}

	err = bpm.ReadSamples(r, eng.cfg.BlockSize, func(block []float32) error {
		eng.write(block)
		return nil
	})
	if err != nil && !errors.Is(err, bpm.ErrTruncatedInput) {
		return nil, err
	}
	return eng.result(""), nil
}

// AnalyzePCM is Analyze for an in-memory PCM buffer.
func AnalyzePCM(pcm []float32, opts Options) (*Result, error) {
	eng, err := newEngine(opts)
	if err != nil {
		return nil, err
	}
	eng.write(pcm)
	return eng.result(""), nil
}

type engine struct {
	cfg     Options
	tempo   *bpm.Processor
	energy  *bpm.EnergyAccumulator
	stream  *key.FrameStream
	frames  []key.Frame
	samples int64
}

func newEngine(opts Options) (*engine, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()

	proc, err := bpm.NewChecked(
		bpm.WithRate(bpm.Rate(opts.Rate)),
		bpm.WithInterval(opts.TempoInterval),
		bpm.WithTempoRange(opts.MinBPM, opts.MaxBPM),
		bpm.WithScanSteps(opts.ScanSteps),
		bpm.WithScanSamples(opts.ScanSamples),
	)
	if err != nil {
		return nil, err
	}

	ex := key.NewExtractor(opts.Rate)
	ex.FrameSize = opts.FrameSize
	ex.HopSize = opts.HopSize
	ex.Whitening = opts.Whiten
	stream, err := ex.NewStream()
	if err != nil {
		return nil, err
	}

	return &engine{
		cfg:    opts,
		tempo:  proc,
		energy: proc.NewEnergyAccumulator(),
		stream: stream,
	}, nil
}

func (e *engine) write(pcm []float32) {
	if len(pcm) == 0 {
		return
	}
	e.energy.Write(pcm)
	e.samples += int64(len(pcm))
	e.frames = append(e.frames, e.stream.Write(pcm)...)
}

func (e *engine) result(path string) *Result {
	res := &Result{
		Path:     path,
		Duration: float64(e.samples) / e.cfg.Rate,
	}

	est := e.tempo.Estimate(e.energy.Values())
	res.Tempo = Tempo{
		BPM:         float64(est.BPM),
		Confidence:  est.Confidence,
		AtRangeEdge: est.AtRangeEdge,
		Valid:       est.Valid,
	}

	correlator := key.NewCorrelator(e.cfg.Key)
	global := correlator.Analyze(meanChroma(e.frames), len(e.frames))
	res.Key = KeyAnalysis{
		Domain:     global.Domain,
		Confidence: global.Confidence,
		Margin:     global.Margin,
		Present:    nonNil(global.Present),
		Segments:   e.segments(correlator),
	}
	res.Key.Modulations = modulations(res.Key.Segments, e.cfg.MinModulationSeconds)
	return res
}

func (e *engine) segments(correlator *key.Correlator) []KeySegment {
	segOpts := e.cfg.Segment.WithDefaults()
	points := aggregate(e.frames, segOpts.WindowSeconds)
	spans := segment.Detect(points, float64(e.samples)/e.cfg.Rate, e.cfg.Segment)

	out := make([]KeySegment, 0, len(spans))
	sections := make([]segment.Section, 0, len(spans))

	idx := 0
	for _, sp := range spans {
		var sum key.Chroma
		var rms float64
		count := 0

		for idx < len(e.frames) && e.frames[idx].Time < sp.Start {
			idx++
		}
		for j := idx; j < len(e.frames) && e.frames[j].Time < sp.End; j++ {
			sum = sum.Add(e.frames[j].Chroma)
			rms += e.frames[j].RMS
			count++
			idx = j + 1
		}

		var vector key.Chroma
		var meanRMS float64
		if count > 0 {
			vector = sum.Scale(1 / float64(count))
			meanRMS = rms / float64(count)
		}

		assess := correlator.Analyze(vector, count)
		out = append(out, KeySegment{
			Start:      sp.Start,
			End:        sp.End,
			Domain:     assess.Domain,
			Confidence: assess.Confidence,
			Margin:     assess.Margin,
			Frames:     count,
			Keys:       nonNil(assess.Present),
		})

		v := make([]float64, 12)
		for i := range v {
			v[i] = vector[i]
		}
		sections = append(sections, segment.Section{
			Start:  sp.Start,
			End:    sp.End,
			Vector: v,
			RMS:    meanRMS,
		})
	}

	normaliseEnergy(out, sections)
	label(e.cfg, out, sections)
	return out
}

func normaliseEnergy(segments []KeySegment, sections []segment.Section) {
	var max float64
	for _, s := range sections {
		if s.RMS > max {
			max = s.RMS
		}
	}
	if max <= 0 {
		return
	}
	for i := range segments {
		segments[i].Energy = sections[i].RMS / max
	}
}

func label(cfg Options, segments []KeySegment, sections []segment.Section) {
	if !cfg.Labels || len(segments) == 0 {
		return
	}
	labeller := cfg.Labeller
	if labeller == nil {
		labeller = segment.HeuristicLabeller{Options: cfg.LabellerOptions}
	}
	labels := labeller.Labels(sections)
	if len(labels) != len(segments) {
		return
	}
	for i := range segments {
		segments[i].Label = labels[i]
	}
}

// modulations reports key changes between confident sections.
//
// Only sections lasting at least minSeconds whose key is strong or moderate
// can start a modulation. Ambiguous sections are skipped rather than treated
// as a key change, which is what stops a momentary correlation wobble from
// producing a false modulation.
func modulations(segments []KeySegment, minSeconds float64) []Modulation {
	var out []Modulation
	var last *KeySegment
	for i := range segments {
		s := &segments[i]
		if s.Domain == "" || s.End-s.Start < minSeconds {
			continue
		}
		if s.Confidence != key.ConfidenceStrong && s.Confidence != key.ConfidenceModerate {
			continue
		}
		if last != nil && s.Domain != last.Domain {
			out = append(out, Modulation{
				At:         s.Start,
				From:       last.Domain,
				To:         s.Domain,
				Confidence: s.Confidence,
			})
		}
		last = s
	}
	return out
}

func meanChroma(frames []key.Frame) key.Chroma {
	var sum key.Chroma
	for _, f := range frames {
		sum = sum.Add(f.Chroma)
	}
	if len(frames) == 0 {
		return sum
	}
	return sum.Scale(1 / float64(len(frames)))
}

// aggregate folds chroma frames into fixed windows so the self-similarity
// matrix stays small regardless of song length.
func aggregate(frames []key.Frame, window float64) []segment.Point {
	if len(frames) == 0 || !(window > 0) {
		return nil
	}
	type bucket struct {
		sum   key.Chroma
		rms   float64
		count int
	}

	var buckets []bucket
	for _, f := range frames {
		i := int(f.Time / window)
		if i < 0 {
			continue
		}
		for len(buckets) <= i {
			buckets = append(buckets, bucket{})
		}
		buckets[i].sum = buckets[i].sum.Add(f.Chroma)
		buckets[i].rms += f.RMS
		buckets[i].count++
	}

	points := make([]segment.Point, 0, len(buckets))
	for i, b := range buckets {
		if b.count == 0 {
			continue
		}
		v := make([]float64, 12)
		for k := range v {
			v[k] = b.sum[k] / float64(b.count)
		}
		points = append(points, segment.Point{
			Time:   float64(i) * window,
			Vector: v,
			RMS:    b.rms / float64(b.count),
		})
	}
	return points
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
