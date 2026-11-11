package key

import (
	"math"
	"testing"
)

func sinePCM(rate, freq, amp, seconds float64) []float32 {
	n := int(rate * seconds)
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/rate))
	}
	return out
}

func mix(signals ...[]float32) []float32 {
	n := len(signals[0])
	out := make([]float32, n)
	for _, s := range signals {
		for i := 0; i < n && i < len(s); i++ {
			out[i] += s[i]
		}
	}
	return out
}

func TestNewExtractorDefaults(t *testing.T) {
	e := NewExtractor(0)
	if e.Rate != 44100 {
		t.Fatalf("Rate = %v, want 44100", e.Rate)
	}
	if e.FrameSize != DefaultFrameSize || e.HopSize != DefaultHopSize {
		t.Fatalf("unexpected frame defaults: %+v", e)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestExtractorValidate(t *testing.T) {
	base := func() *Extractor { return NewExtractor(44100) }

	cases := map[string]func(*Extractor){
		"zero rate":        func(e *Extractor) { e.Rate = 0 },
		"negative rate":    func(e *Extractor) { e.Rate = -1 },
		"non power of two": func(e *Extractor) { e.FrameSize = 3000 },
		"frame too small":  func(e *Extractor) { e.FrameSize = 32 },
		"zero hop":         func(e *Extractor) { e.HopSize = 0 },
		"hop larger":       func(e *Extractor) { e.HopSize = e.FrameSize * 2 },
		"zero min hz":      func(e *Extractor) { e.MinHz = 0 },
		"max below min":    func(e *Extractor) { e.MaxHz = e.MinHz / 2 },
		"above nyquist":    func(e *Extractor) { e.MaxHz = e.Rate },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := base()
			mutate(e)
			if err := e.Validate(); err == nil {
				t.Fatalf("Validate() = nil for %+v", e)
			}
		})
	}

	var nilExtractor *Extractor
	if err := nilExtractor.Validate(); err == nil {
		t.Fatal("nil extractor validated")
	}
}

func TestChromaOfA440(t *testing.T) {
	e := NewExtractor(44100)
	frames, err := e.Extract(sinePCM(44100, 440, 0.5, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}

	var sum Chroma
	for _, f := range frames {
		sum = sum.Add(f.Chroma)
	}

	best, bestVal := 0, 0.0
	for i, v := range sum {
		if v > bestVal {
			best, bestVal = i, v
		}
	}
	if best != 9 {
		t.Fatalf("dominant pitch class = %q, want A", PitchClasses[best])
	}
	if got := sum[best] / sum[(best+3)%12]; got < 3 {
		t.Fatalf("A is only %.2fx its relative C; chroma is too flat: %v", got, sum)
	}
}

func TestChromaOfCMajorTriad(t *testing.T) {
	e := NewExtractor(44100)
	pcm := mix(
		sinePCM(44100, 261.63, 0.2, 2), // C4
		sinePCM(44100, 329.63, 0.2, 2), // E4
		sinePCM(44100, 392.00, 0.2, 2), // G4
	)
	frames, err := e.Extract(pcm)
	if err != nil {
		t.Fatal(err)
	}

	var sum Chroma
	for _, f := range frames {
		sum = sum.Add(f.Chroma)
	}
	sum = sum.Normalize()

	for _, pc := range []int{0, 4, 7} {
		if sum[pc] < 0.5 {
			t.Errorf("chord tone %q = %.3f, want >= 0.5 (chroma %v)", PitchClasses[pc], sum[pc], sum)
		}
	}
	for _, pc := range []int{1, 3, 6, 8, 10} {
		if sum[pc] > 0.25 {
			t.Errorf("non-chord tone %q = %.3f, want <= 0.25 (chroma %v)", PitchClasses[pc], sum[pc], sum)
		}
	}

	got := NewCorrelator(Options{}).Analyze(sum, len(frames))
	if got.Domain != "C major" {
		t.Fatalf("Analyze = %q, want C major (present %+v)", got.Domain, got.Present)
	}
}

func TestFramesAreNormalizedAndTimed(t *testing.T) {
	e := NewExtractor(44100)
	frames, err := e.Extract(sinePCM(44100, 440, 0.5, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 2 {
		t.Fatalf("got %d frames, want several", len(frames))
	}
	for i, f := range frames {
		var max float64
		for _, v := range f.Chroma {
			if v > max {
				max = v
			}
		}
		if math.Abs(max-1) > 1e-9 {
			t.Fatalf("frame %d max chroma = %v, want 1", i, max)
		}
		want := float64(i*DefaultHopSize) / 44100
		if math.Abs(f.Time-want) > 1e-9 {
			t.Fatalf("frame %d time = %v, want %v", i, f.Time, want)
		}
		if f.RMS <= 0 {
			t.Fatalf("frame %d RMS = %v, want > 0", i, f.RMS)
		}
	}
}

func TestSilenceFrames(t *testing.T) {
	e := NewExtractor(44100)
	frames, err := e.Extract(make([]float32, 44100))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	for _, f := range frames {
		if f.RMS != 0 {
			t.Fatalf("RMS = %v, want 0", f.RMS)
		}
		if f.Chroma.Sum() != 0 {
			t.Fatalf("chroma = %v, want zero", f.Chroma)
		}
	}
}

func TestShortInputProducesNoFrames(t *testing.T) {
	e := NewExtractor(44100)
	frames, err := e.Extract(make([]float32, DefaultFrameSize-1))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 0 {
		t.Fatalf("got %d frames for a partial frame", len(frames))
	}
}

func TestStreamMatchesBatch(t *testing.T) {
	e := NewExtractor(44100)
	pcm := mix(
		sinePCM(44100, 261.63, 0.2, 2),
		sinePCM(44100, 392.00, 0.2, 2),
	)

	batch, err := e.Extract(pcm)
	if err != nil {
		t.Fatal(err)
	}

	stream, err := e.NewStream()
	if err != nil {
		t.Fatal(err)
	}
	var chunked []Frame
	for i := 0; i < len(pcm); i += 1000 {
		end := i + 1000
		if end > len(pcm) {
			end = len(pcm)
		}
		chunked = append(chunked, stream.Write(pcm[i:end])...)
	}

	if len(chunked) != len(batch) {
		t.Fatalf("streamed %d frames, batched %d", len(chunked), len(batch))
	}
	for i := range batch {
		if chunked[i] != batch[i] {
			t.Fatalf("frame %d differs between stream and batch", i)
		}
	}
	if got := stream.Duration(); math.Abs(got-2) > 1e-9 {
		t.Fatalf("Duration = %v, want 2", got)
	}
	if got := stream.Samples(); got != int64(len(pcm)) {
		t.Fatalf("Samples = %d, want %d", got, len(pcm))
	}
}

func TestWhiteningRuns(t *testing.T) {
	e := NewExtractor(44100)
	e.Whitening = true
	frames, err := e.Extract(sinePCM(44100, 440, 0.5, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("no frames")
	}
	for _, f := range frames {
		if math.IsNaN(f.Chroma.Sum()) || f.Chroma.Sum() <= 0 {
			t.Fatalf("invalid chroma with whitening: %v", f.Chroma)
		}
	}
}
