package segment

import (
	"math"
	"testing"
)

// blockPoints builds points whose feature vectors switch at the given
// boundaries (in seconds).
func blockPoints(window float64, duration float64, boundaries []float64, keys [][]float64) []Point {
	var pts []Point
	seg := 0
	for t := 0.0; t < duration; t += window {
		for seg+1 < len(keys) && t >= boundaries[seg] {
			seg++
		}
		v := keys[seg]
		pts = append(pts, Point{Time: t, Vector: v, RMS: 0.5})
	}
	return pts
}

func vec(seed float64) []float64 {
	out := make([]float64, 12)
	for i := range out {
		out[i] = math.Mod(seed*float64(i+1), 7) + 0.1
	}
	return out
}

func TestDetectFindsBlockBoundaries(t *testing.T) {
	// A (sectionA) -> B (sectionB) -> A
	a, b := vec(1), vec(3)
	points := blockPoints(0.5, 60, []float64{20, 40}, [][]float64{a, b, a})

	spans := Detect(points, 60, Options{})
	if len(spans) != 3 {
		t.Fatalf("got %d spans, want 3: %+v", len(spans), spans)
	}

	if math.Abs(spans[0].Start) > 1e-9 || math.Abs(spans[0].End-20) > 1.5 {
		t.Errorf("first boundary = %v, want ~20", spans[0].End)
	}
	if math.Abs(spans[1].End-40) > 1.5 {
		t.Errorf("second boundary = %v, want ~40", spans[1].End)
	}
	if math.Abs(spans[2].End-60) > 1e-9 {
		t.Errorf("last span ends at %v, want 60", spans[2].End)
	}
	for _, s := range spans {
		if s.Duration() <= 0 {
			t.Errorf("non-positive span: %+v", s)
		}
	}
}

func TestDetectStationaryInput(t *testing.T) {
	a := vec(2)
	points := blockPoints(0.5, 30, nil, [][]float64{a})

	spans := Detect(points, 30, Options{})
	if len(spans) != 1 {
		t.Fatalf("got %d spans for stationary input, want 1: %+v", len(spans), spans)
	}
	if spans[0].Start != 0 || math.Abs(spans[0].End-30) > 1e-9 {
		t.Fatalf("span = %+v, want [0,30]", spans[0])
	}
}

func TestDetectRespectsMinimumSegment(t *testing.T) {
	// Alternate every 4 seconds; with an 8 second minimum the detector must
	// not emit a boundary every 4 seconds.
	keys := [][]float64{vec(1), vec(5), vec(9), vec(1), vec(5), vec(9), vec(1), vec(5)}
	points := blockPoints(0.5, 32, []float64{4, 8, 12, 16, 20, 24, 28}, keys)

	opts := Options{MinSegmentSeconds: 8}
	spans := Detect(points, 32, opts)
	for _, s := range spans {
		if s.Duration() < 8-1e-9 {
			t.Fatalf("span %+v is shorter than the 8s minimum (all: %+v)", s, spans)
		}
	}
}

func TestDetectShortAndEmptyInput(t *testing.T) {
	if got := Detect(nil, 10, Options{}); got != nil {
		t.Fatalf("Detect(nil) = %+v, want nil", got)
	}

	one := []Point{{Time: 0, Vector: vec(1), RMS: 0.5}}
	got := Detect(one, 10, Options{})
	if len(got) != 1 || got[0].End != 10 {
		t.Fatalf("single point = %+v, want one span ending at 10", got)
	}
}

func TestDetectRejectsInvalidOptions(t *testing.T) {
	points := blockPoints(0.5, 10, nil, [][]float64{vec(1)})
	if got := Detect(points, 10, Options{MinSegmentSeconds: 0.1}); got != nil {
		t.Fatalf("Detect with min segment below the window = %+v, want nil", got)
	}
}

func TestDownsampleBoundsMatrix(t *testing.T) {
	a := vec(1)
	var points []Point
	for i := 0; i < 5000; i++ {
		points = append(points, Point{Time: float64(i) * 0.5, Vector: a, RMS: 0.5})
	}
	got := downsample(points, 300)
	if len(got) > 300 {
		t.Fatalf("downsample returned %d points, want <= 300", len(got))
	}
	if len(got) == 0 {
		t.Fatal("downsample returned nothing")
	}
	// Time origin must be preserved for boundary placement.
	if got[0].Time != 0 {
		t.Fatalf("first time = %v, want 0", got[0].Time)
	}
}

func TestDetectHomogeneousLoop(t *testing.T) {
	// Nearly identical chroma with tiny jitter and constant loudness: a loop
	// or a drone. Novelty peaks here are noise and must not become segments.
	base := vec(2)
	var points []Point
	for i := 0; i < 200; i++ {
		v := make([]float64, len(base))
		for k := range base {
			v[k] = base[k] * (1 + 0.002*math.Sin(float64(i*7+k)))
		}
		points = append(points, Point{Time: float64(i) * 0.5, Vector: v, RMS: 0.5})
	}

	spans := Detect(points, 100, Options{})
	if len(spans) != 1 {
		t.Fatalf("got %d spans for a homogeneous loop, want 1: %+v", len(spans), spans)
	}
}

func TestDetectEnergyOnlyChange(t *testing.T) {
	// Same harmony throughout, but the second half is much louder: a real
	// structural change that chroma alone cannot see.
	a := vec(3)
	var points []Point
	for i := 0; i < 120; i++ {
		rms := 0.2
		if i >= 60 {
			rms = 0.9
		}
		points = append(points, Point{Time: float64(i) * 0.5, Vector: a, RMS: rms})
	}

	spans := Detect(points, 60, Options{})
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(spans), spans)
	}
	if math.Abs(spans[0].End-30) > 2 {
		t.Fatalf("boundary at %v, want ~30", spans[0].End)
	}
}

func TestBoundariesReportsChangeWithStrength(t *testing.T) {
	a, b := vec(1), vec(4)
	points := blockPoints(0.5, 60, []float64{30}, [][]float64{a, b})

	got := Boundaries(points, Options{})
	if len(got) != 1 {
		t.Fatalf("got %d boundaries, want 1: %+v", len(got), got)
	}
	if math.Abs(got[0].Time-30) > 1.5 {
		t.Fatalf("boundary at %v, want ~30", got[0].Time)
	}
	if got[0].Strength <= 0 {
		t.Fatalf("strength = %v, want > 0", got[0].Strength)
	}
}

func TestNoveltyPeaksAtChange(t *testing.T) {
	a, b := vec(1), vec(4)
	points := blockPoints(0.5, 60, []float64{30}, [][]float64{a, b})

	nov := Novelty(points, Options{})
	if len(nov) != len(points) {
		t.Fatalf("novelty has %d values, want %d", len(nov), len(points))
	}
	best, bestVal := 0, math.Inf(-1)
	for i, v := range nov {
		if v > bestVal {
			best, bestVal = i, v
		}
	}
	want := 30 / 0.5
	if math.Abs(float64(best)-want) > 8 {
		t.Fatalf("novelty peak at point %d, want ~%.0f", best, want)
	}
}
