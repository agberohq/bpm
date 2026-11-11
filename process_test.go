package bpm

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
)

func TestNew(t *testing.T) {
	p := New()

	if p.Rate != DefaultRate {
		t.Fatalf("Rate = %v, want %v", p.Rate, DefaultRate)
	}
	if p.Interval != DefaultInterval {
		t.Fatalf("Interval = %v, want %v", p.Interval, DefaultInterval)
	}
	if p.MinBPM != DefaultMinBPM {
		t.Fatalf("MinBPM = %v, want %v", p.MinBPM, DefaultMinBPM)
	}
	if p.MaxBPM != DefaultMaxBPM {
		t.Fatalf("MaxBPM = %v, want %v", p.MaxBPM, DefaultMaxBPM)
	}
	if p.Steps != DefaultScanSteps {
		t.Fatalf("Steps = %v, want %v", p.Steps, DefaultScanSteps)
	}
	if p.Samples != DefaultScanSamples {
		t.Fatalf("Samples = %v, want %v", p.Samples, DefaultScanSamples)
	}
	if p.rand == nil {
		t.Fatal("rand is nil")
	}
}

func TestBPMToIntervalAndBack(t *testing.T) {
	p := New()

	for _, want := range []BPM{84, 90, 100, 110, 120, 128, 140, 146} {
		got := p.IntervalToBPM(p.BPMToInterval(want))
		if math.Abs(float64(got-want)) > 1e-9 {
			t.Fatalf("round trip: bpm=%v got=%v", want, got)
		}
	}
}

func TestKnownBPMIntervals(t *testing.T) {
	p := New()

	tests := []struct {
		bpm      BPM
		interval Interval
	}{
		{60, 344.53125},
		{120, 172.265625},
	}

	for _, tt := range tests {
		got := p.BPMToInterval(tt.bpm)
		if math.Abs(float64(got-tt.interval)) > 1e-9 {
			t.Errorf("BPMToInterval(%v) = %v, want %v", tt.bpm, got, tt.interval)
		}
	}
}

func TestEnergyEmpty(t *testing.T) {
	p := New()
	if got := p.Energy(nil); got != nil {
		t.Fatalf("Energy(nil) = %v, want nil", got)
	}
}

func TestEnergySamplesEveryInterval(t *testing.T) {
	p := New()

	input := make([]float32, DefaultInterval*3)
	for i := range input {
		input[i] = 1
	}

	got := p.Energy(input)
	if len(got) != 3 {
		t.Fatalf("Energy returned %d samples, want 3", len(got))
	}
	for i, v := range got {
		if v <= 0 {
			t.Errorf("energy[%d] = %v, want positive", i, v)
		}
	}
}

func TestEnergyAttack(t *testing.T) {
	p := New(WithInterval(10))

	input := make([]float32, 20)
	for i := range input {
		input[i] = 1
	}

	got := p.Energy(input)
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	if got[1] <= got[0] {
		t.Fatalf("energy should increase during attack: %f <= %f", got[1], got[0])
	}
}

func TestEnergyRelease(t *testing.T) {
	p := New(WithInterval(10))

	input := make([]float32, 30)
	for i := 0; i < 10; i++ {
		input[i] = 1
	}

	got := p.Energy(input)
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
	if got[1] >= got[0] {
		t.Fatalf("energy should decrease during release: %f >= %f", got[1], got[0])
	}
	if got[2] >= got[1] {
		t.Fatalf("energy should continue decreasing: %f >= %f", got[2], got[1])
	}
}

func TestEnergyTrace(t *testing.T) {
	var buf bytes.Buffer
	p := New(WithEnergyTrace(&buf))

	input := make([]float32, DefaultInterval)
	for i := range input {
		input[i] = 1
	}

	if got := p.Energy(input); len(got) != 1 {
		t.Fatalf("got %d samples, want 1", len(got))
	}
	if buf.Len() == 0 {
		t.Fatal("expected energy diagnostic output")
	}
}

func TestEnergyMatchesManualCalculation(t *testing.T) {
	p := New()

	input := make([]float32, DefaultInterval)
	for i := range input {
		input[i] = 1
	}

	got := p.Energy(input)

	var expected float32
	for range input {
		expected += (1 - expected) / 8
	}

	if math.Abs(float64(got[0]-expected)) > 1e-7 {
		t.Fatalf("energy = %f, want %f", got[0], expected)
	}
}

func TestSample(t *testing.T) {
	p := New()
	nrg := []float32{10, 20, 30}

	tests := []struct {
		name   string
		offset float64
		want   float64
	}{
		{"negative", -1, 0},
		{"first", 0, 10},
		{"fraction floors", 0.9, 10},
		{"second", 1, 20},
		{"second fraction", 1.9, 20},
		{"third", 2, 30},
		{"past end", 3, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Sample(nrg, tt.offset); got != tt.want {
				t.Fatalf("Sample(%v) = %v, want %v", tt.offset, got, tt.want)
			}
		})
	}
}

func TestAutoDifferenceEmpty(t *testing.T) {
	p := New()
	if got := p.AutoDifference(nil, 2); got != 0 {
		t.Fatalf("got %v, want 0", got)
	}
}

func TestAutoDifferenceDeterministic(t *testing.T) {
	p := New(WithRand(rand.New(rand.NewSource(42))))

	nrg := make([]float32, 10000)
	for i := range nrg {
		nrg[i] = float32(math.Sin(float64(i) * 0.1))
	}

	got := p.AutoDifference(nrg, 10)
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("AutoDifference returned invalid value: %v", got)
	}
}

func TestAutoDifferenceConstantSignal(t *testing.T) {
	p := New(WithRand(rand.New(rand.NewSource(42))))

	nrg := make([]float32, 10000)
	for i := range nrg {
		nrg[i] = 1
	}

	if got := p.AutoDifference(nrg, 10); math.Abs(got) > 1e-9 {
		t.Fatalf("constant signal autodifference = %v, want 0", got)
	}
}

func TestAutoDifferenceDeterministicSeed(t *testing.T) {
	nrg := make([]float32, 10000)
	for i := range nrg {
		nrg[i] = float32(math.Sin(float64(i) * 0.05))
	}

	a := New(WithRand(rand.New(rand.NewSource(1234))))
	b := New(WithRand(rand.New(rand.NewSource(1234))))

	gotA := a.AutoDifference(nrg, 4)
	gotB := b.AutoDifference(nrg, 4)

	if gotA != gotB {
		t.Fatalf("same seed produced different values: %v != %v", gotA, gotB)
	}
}

func TestReadFloat32(t *testing.T) {
	values := []float32{0, 1, -1, 0.5, -0.25, float32(math.Pi)}

	var buf bytes.Buffer
	for _, v := range values {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ReadFloat32(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(values) {
		t.Fatalf("got %d samples, want %d", len(got), len(values))
	}
	for i := range values {
		if got[i] != values[i] {
			t.Errorf("sample[%d] = %v, want %v", i, got[i], values[i])
		}
	}
}

func TestReadFloat32Empty(t *testing.T) {
	got, err := ReadFloat32(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d samples, want 0", len(got))
	}
}

func TestScanEmpty(t *testing.T) {
	if got := New().Scan(nil); !math.IsNaN(float64(got)) {
		t.Fatalf("got %v, want NaN", got)
	}
}

func TestScanInvalidConfig(t *testing.T) {
	nrg := make([]float32, 1000)

	cases := map[string]*Processor{
		"zero steps":   New(WithScanSteps(0)),
		"zero samples": New(WithScanSamples(0)),
		"min == max":   New(WithTempoRange(120, 120)),
		"min > max":    New(WithTempoRange(146, 84)),
		"min zero":     New(WithTempoRange(0, 146)),
		"max zero":     New(WithTempoRange(84, 0)),
		"min negative": New(WithTempoRange(-84, 146)),
	}

	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if got := p.Scan(nrg); !math.IsNaN(float64(got)) {
				t.Fatalf("got %v, want NaN", got)
			}
		})
	}
}

func TestScanTrace(t *testing.T) {
	var buf bytes.Buffer
	p := New(
		WithDiffTrace(&buf),
		WithScanSteps(10),
		WithScanSamples(2),
	)

	nrg := make([]float32, 10000)
	for i := range nrg {
		nrg[i] = float32(i % 64)
	}
	p.Scan(nrg)

	if buf.Len() == 0 {
		t.Fatal("expected diff diagnostic output")
	}
}

// TestScanFindsSyntheticPeriod validates that the objective function has a
// minimum at the true tempo. This is the only test that exercises the scan
// end-to-end on a signal with a known answer.
func TestScanFindsSyntheticPeriod(t *testing.T) {
	const wantBPM = BPM(120)

	for _, seed := range []int64{1, 7, 42, 1234} {
		t.Run("", func(t *testing.T) {
			p := New(
				WithRand(rand.New(rand.NewSource(seed))),
				WithScanSteps(256),
				WithScanSamples(256),
			)

			period := p.BPMToInterval(wantBPM)

			const length = 8192
			nrg := make([]float32, length)
			for i := range nrg {
				pos := float64(i) / float64(period)
				frac := pos - math.Floor(pos)
				distance := math.Abs(frac - 0.5)
				peak := math.Max(0, 1-8*distance)
				nrg[i] = float32(0.1 + peak)
			}

			got := p.Scan(nrg)
			if math.Abs(float64(got-wantBPM)) > 2.0 {
				t.Fatalf("Scan = %v BPM, want ~%v", got, wantBPM)
			}
		})
	}
}
