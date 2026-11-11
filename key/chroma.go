package key

import (
	"fmt"
	"math"

	"github.com/agberohq/bpm/internal/fft"
)

const (
	// DefaultFrameSize is the STFT window length in samples (~186 ms at
	// 44.1 kHz). Low notes need the extra resolution: at 4096 samples a bass
	// note near 155 Hz falls between bins and is mapped to the wrong pitch
	// classes.
	DefaultFrameSize = 8192
	// DefaultHopSize is the STFT hop in samples (~93 ms at 44.1 kHz).
	// Overlapping windows are not needed for chroma, and the wider hop halves
	// the FFT work.
	DefaultHopSize = 4096
	// DefaultMinHz / DefaultMaxHz bound the frequencies folded into chroma.
	// C2..C7 covers the range that carries tonal information in most music
	// while avoiding rumble and cymbal wash.
	DefaultMinHz = 65.41
	DefaultMaxHz = 2093.0
)

// Frame is one chroma observation.
type Frame struct {
	// Time is the frame start in seconds.
	Time float64 `json:"time"`
	// RMS is the frame loudness, used for segmentation and silence checks.
	RMS float64 `json:"rms"`
	// Chroma is normalised so the loudest pitch class is 1.
	Chroma Chroma `json:"chroma"`
}

// Extractor computes chroma frames from mono float32 PCM.
//
// The extractor is stateless; use NewStream for incremental input.
type Extractor struct {
	Rate      float64
	FrameSize int
	HopSize   int
	MinHz     float64
	MaxHz     float64
	// Whitening flattens narrow spectral peaks by dividing each bin by the
	// local spectral average. It reduces the influence of broadband
	// percussion, at the cost of some sensitivity to quiet tonal content.
	Whitening bool
}

// NewExtractor returns an Extractor for rate with default parameters.
func NewExtractor(rate float64) *Extractor {
	if !(rate > 0) {
		rate = 44100
	}
	return &Extractor{
		Rate:      rate,
		FrameSize: DefaultFrameSize,
		HopSize:   DefaultHopSize,
		MinHz:     DefaultMinHz,
		MaxHz:     DefaultMaxHz,
	}
}

// Validate checks the extractor configuration.
func (e *Extractor) Validate() error {
	if e == nil {
		return fmt.Errorf("key: nil extractor")
	}
	switch {
	case !(e.Rate > 0):
		return fmt.Errorf("key: sample rate must be > 0, got %v", e.Rate)
	case e.FrameSize < 64 || e.FrameSize&(e.FrameSize-1) != 0:
		return fmt.Errorf("key: frame size must be a power of two >= 64, got %d", e.FrameSize)
	case e.HopSize <= 0 || e.HopSize > e.FrameSize:
		return fmt.Errorf("key: hop size must be in (0, frame size], got %d", e.HopSize)
	case !(e.MinHz > 0):
		return fmt.Errorf("key: minimum frequency must be > 0, got %v", e.MinHz)
	case !(e.MaxHz > e.MinHz):
		return fmt.Errorf("key: maximum frequency %v must exceed minimum %v", e.MaxHz, e.MinHz)
	case e.MaxHz >= e.Rate/2:
		return fmt.Errorf("key: maximum frequency %v must be below the Nyquist frequency %v", e.MaxHz, e.Rate/2)
	}
	return nil
}

// Extract returns the chroma frames of an in-memory PCM buffer.
func (e *Extractor) Extract(pcm []float32) ([]Frame, error) {
	s, err := e.NewStream()
	if err != nil {
		return nil, err
	}
	return s.Write(pcm), nil
}

// NewStream returns an incremental frame extractor.
func (e *Extractor) NewStream() (*FrameStream, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	plan, err := fft.NewPlan(e.FrameSize)
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}

	window := make([]float64, e.FrameSize)
	for i := range window {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(e.FrameSize-1)))
	}

	return &FrameStream{
		ex:     e,
		plan:   plan,
		window: window,
		frame:  make([]float64, e.FrameSize),
		spec:   make([]complex128, e.FrameSize/2+1),
		work:   make([]complex128, e.FrameSize),
		bin:    make([]float64, e.FrameSize/2+1),
	}, nil
}

// FrameStream converts PCM written in chunks into chroma frames.
//
// It is not safe for concurrent use. Trailing samples shorter than one frame
// are ignored: a partial frame has no usable spectral resolution.
type FrameStream struct {
	ex     *Extractor
	plan   *fft.Plan
	window []float64
	frame  []float64
	spec   []complex128
	work   []complex128
	bin    []float64

	buf     []float32
	off     int
	samples int64
	time    float64
}

// Write consumes pcm and returns any frames that became complete.
func (s *FrameStream) Write(pcm []float32) []Frame {
	if s == nil || len(pcm) == 0 {
		return nil
	}
	s.samples += int64(len(pcm))
	s.buf = append(s.buf, pcm...)

	var frames []Frame
	for len(s.buf)-s.off >= s.ex.FrameSize {
		frames = append(frames, s.frameAt(s.buf[s.off:s.off+s.ex.FrameSize]))
		s.off += s.ex.HopSize
	}

	if s.off > 0 {
		s.buf = append(s.buf[:0], s.buf[s.off:]...)
		s.off = 0
	}
	return frames
}

// Samples returns the number of PCM samples consumed so far.
func (s *FrameStream) Samples() int64 {
	if s == nil {
		return 0
	}
	return s.samples
}

// Duration returns the duration in seconds of the PCM consumed so far.
func (s *FrameStream) Duration() float64 {
	if s == nil || s.ex == nil {
		return 0
	}
	return float64(s.samples) / s.ex.Rate
}

func (s *FrameStream) frameAt(pcm []float32) Frame {
	var energy float64
	for i, v := range pcm {
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			f = 0
		}
		energy += f * f
		s.frame[i] = f * s.window[i]
	}

	s.plan.PowerSpectrum(s.frame, s.spec, s.work)

	half := s.ex.FrameSize / 2
	if s.ex.Whitening {
		for k := 1; k <= half; k++ {
			s.bin[k] = math.Sqrt(real(s.spec[k]))
		}
	}

	var chroma Chroma
	binHz := s.ex.Rate / float64(s.ex.FrameSize)
	for k := 1; k <= half; k++ {
		freq := float64(k) * binHz
		if freq < s.ex.MinHz || freq > s.ex.MaxHz {
			continue
		}
		mag := math.Sqrt(real(s.spec[k]))
		if s.ex.Whitening {
			var acc float64
			var n int
			for j := k - 4; j <= k+4; j++ {
				if j >= 1 && j <= half {
					acc += s.bin[j]
					n++
				}
			}
			if n > 0 {
				mag /= acc/float64(n) + 1e-12
			}
		}
		if mag <= 0 {
			continue
		}

		// Fold the bin into the two nearest semitones, weighted linearly.
		// Nearest-bin rounding loses bass notes: at low frequencies the FFT
		// bins are wider than a semitone, so a note can land entirely in
		// neighbouring pitch classes.
		midi := 69 + 12*math.Log2(freq/440)
		if midi < 0 {
			continue
		}
		floor := math.Floor(midi)
		frac := midi - floor
		lo := int(floor) % 12
		if lo < 0 {
			lo += 12
		}
		hi := (lo + 1) % 12
		chroma[lo] += mag * (1 - frac)
		chroma[hi] += mag * frac
	}

	fr := Frame{
		Time:   s.time,
		RMS:    math.Sqrt(energy / float64(len(pcm))),
		Chroma: chroma.Normalize(),
	}
	s.time += float64(s.ex.HopSize) / s.ex.Rate
	return fr
}
