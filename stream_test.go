package bpm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func encodeFloat32(t *testing.T, samples []float32) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, v := range samples {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// clickTrack builds mono PCM with a short click every beat.
func clickTrack(rate float64, bpm BPM, seconds float64) []float32 {
	n := int(rate * seconds)
	out := make([]float32, n)
	period := int(rate * secondsPerMinute / float64(bpm))
	for beat := 0; beat*period < n; beat++ {
		start := beat * period
		for i := 0; i < 256 && start+i < n; i++ {
			out[start+i] = float32(0.9 * math.Exp(-float64(i)/40))
		}
	}
	return out
}

func TestReadSamplesBlocks(t *testing.T) {
	samples := make([]float32, 10_000)
	for i := range samples {
		samples[i] = float32(i) / 10_000
	}

	var got []float32
	var sizes []int
	err := ReadSamples(bytes.NewReader(encodeFloat32(t, samples)), 1000, func(block []float32) error {
		sizes = append(sizes, len(block))
		got = append(got, block...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 10 {
		t.Fatalf("got %d blocks, want 10: %v", len(sizes), sizes)
	}
	if len(got) != len(samples) {
		t.Fatalf("got %d samples, want %d", len(got), len(samples))
	}
	for i := range samples {
		if got[i] != samples[i] {
			t.Fatalf("sample %d = %v, want %v", i, got[i], samples[i])
		}
	}
}

func TestReadSamplesTruncated(t *testing.T) {
	data := append(encodeFloat32(t, []float32{0.25, -0.5, 1}), 0xAA, 0xBB)

	var got []float32
	err := ReadSamples(bytes.NewReader(data), 2, func(block []float32) error {
		got = append(got, block...)
		return nil
	})
	if !errors.Is(err, ErrTruncatedInput) {
		t.Fatalf("err = %v, want ErrTruncatedInput", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d complete samples, want 3", len(got))
	}
}

func TestReadSamplesCallbackErrorStops(t *testing.T) {
	stop := errors.New("stop")
	calls := 0
	err := ReadSamples(bytes.NewReader(encodeFloat32(t, make([]float32, 100))), 10, func([]float32) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want stop", err)
	}
	if calls != 1 {
		t.Fatalf("callback called %d times, want 1", calls)
	}
}

func TestReadSamplesNilReader(t *testing.T) {
	if err := ReadSamples(nil, 10, func([]float32) error { return nil }); err == nil {
		t.Fatal("expected error for nil reader")
	}
}

type errorAfterReader struct {
	data []byte
	err  error
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestEnergyReaderMatchesEnergy(t *testing.T) {
	pcm := clickTrack(44100, 120, 12)

	want := New().Energy(pcm)
	if len(want) == 0 {
		t.Fatal("Energy returned no samples")
	}

	got, err := New().EnergyReader(bytes.NewReader(encodeFloat32(t, pcm)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("EnergyReader returned %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEnergyReaderReturnsPartialOnFatalError(t *testing.T) {
	pcm := clickTrack(44100, 120, 12)
	boom := errors.New("boom")
	r := &errorAfterReader{data: encodeFloat32(t, pcm), err: boom}

	nrg, err := New().EnergyReader(r)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(nrg) == 0 {
		t.Fatal("partial energy was discarded")
	}
}

func TestAccumulatorChunkingIsEquivalent(t *testing.T) {
	pcm := clickTrack(44100, 120, 4)
	p := New()

	acc := p.NewEnergyAccumulator()
	for i := 0; i < len(pcm); i += 777 {
		end := i + 777
		if end > len(pcm) {
			end = len(pcm)
		}
		acc.Write(pcm[i:end])
	}

	want := p.Energy(pcm)
	if acc.Len() != len(want) {
		t.Fatalf("accumulator has %d samples, want %d", acc.Len(), len(want))
	}
	for i := range want {
		if acc.Values()[i] != want[i] {
			t.Fatalf("sample %d = %v, want %v", i, acc.Values()[i], want[i])
		}
	}
}

func TestDetectTruncatedInputStillReturnsTempo(t *testing.T) {
	pcm := clickTrack(44100, 120, 12)
	data := append(encodeFloat32(t, pcm), 0x01, 0x02, 0x03)

	got, err := New().Detect(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Detect(truncated) returned error %v, want nil", err)
	}
	if math.Abs(float64(got-120)) > 2 {
		t.Fatalf("Detect = %v BPM, want ~120", got)
	}
}

func TestDetectNoResult(t *testing.T) {
	_, err := New().Detect(bytes.NewReader(encodeFloat32(t, make([]float32, 44100*5))))
	if !errors.Is(err, ErrNoResult) {
		t.Fatalf("Detect(silence) = %v, want ErrNoResult", err)
	}
}

func TestDetectInvalidConfig(t *testing.T) {
	_, err := New(WithInterval(0)).Detect(bytes.NewReader(nil))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Detect with bad config = %v, want ErrInvalidConfig", err)
	}
}

func TestDetectPropagatesReadError(t *testing.T) {
	boom := errors.New("disk on fire")
	_, err := New().Detect(&errorAfterReader{err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("Detect = %v, want the underlying read error", err)
	}
}

func TestDetectEstimateConfidence(t *testing.T) {
	pcm := clickTrack(44100, 120, 20)

	est, err := New().DetectEstimate(bytes.NewReader(encodeFloat32(t, pcm)))
	if err != nil {
		t.Fatal(err)
	}
	if !est.Valid {
		t.Fatal("estimate is invalid")
	}
	if math.Abs(float64(est.BPM-120)) > 2 {
		t.Fatalf("BPM = %v, want ~120", est.BPM)
	}
	if est.Confidence <= 0 || est.Confidence > 1 {
		t.Fatalf("Confidence = %v, want (0,1]", est.Confidence)
	}
}

func TestEstimateReportsRangeEdge(t *testing.T) {
	// With a range whose fast end coincides with the click tempo, the winner
	// sits exactly on MaxBPM and the estimate is flagged as boundary-bound.
	p := New(WithTempoRange(60, 120))
	est := p.Estimate(p.Energy(clickTrack(44100, 120, 20)))
	if !est.Valid {
		t.Fatal("estimate is invalid")
	}
	if !est.AtRangeEdge {
		t.Fatalf("AtRangeEdge = false at the range boundary (BPM=%v)", est.BPM)
	}
	if math.Abs(float64(est.BPM-120)) > 1 {
		t.Fatalf("BPM = %v, want ~120", est.BPM)
	}
}

func TestReadFloat32KeepsPartialOnFatalError(t *testing.T) {
	boom := errors.New("boom")
	r := &errorAfterReader{data: encodeFloat32(t, []float32{1, 2, 3}), err: boom}
	got, err := ReadFloat32(r)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
}
