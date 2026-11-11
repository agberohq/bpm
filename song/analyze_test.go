package song

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"

	"github.com/agberohq/bpm/key"
)

// chordHits renders a triad (with its bass root) played on every beat and
// decaying like a piano chord. It has both the harmonic content for key
// detection and the rhythmic attacks for tempo detection.
func chordHits(rate, seconds, bpm, root float64) []float32 {
	n := int(rate * seconds)
	out := make([]float32, n)
	period := int(math.Round(rate * 60 / bpm))
	hitLen := int(0.35 * rate)
	freqs := []float64{root / 2, root, root * 1.25, root * 1.5}

	for beat := 0; beat*period < n; beat++ {
		start := beat * period
		for i := 0; i < hitLen && start+i < n; i++ {
			env := math.Exp(-float64(i) / (0.12 * rate))
			var s float64
			for _, f := range freqs {
				s += 0.12 * math.Sin(2*math.Pi*f*float64(i)/rate)
			}
			out[start+i] += float32(env * s)
		}
	}
	return out
}

func concat(parts ...[]float32) []float32 {
	var out []float32
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestAnalyzeDetectsTempoKeyAndModulation(t *testing.T) {
	const rate = 44100.0

	// C major (C E G) for 30 s, then E-flat major (Eb G Bb) for 30 s.
	pcm := concat(
		chordHits(rate, 30, 120, 261.63),
		chordHits(rate, 30, 120, 311.13),
	)

	res, err := AnalyzePCM(pcm, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}

	if !res.Tempo.Valid {
		t.Fatalf("tempo is invalid: %+v", res.Tempo)
	}
	if math.Abs(res.Tempo.BPM-120) > 1 {
		t.Errorf("BPM = %v, want ~120", res.Tempo.BPM)
	}
	if math.Abs(res.Duration-60) > 1e-6 {
		t.Errorf("Duration = %v, want 60", res.Duration)
	}

	// The global key is computed over the whole song, where C major and
	// Eb major overlap on C minor's notes. It must reach a verdict or admit
	// that it cannot, but either way it must not be empty-by-accident.
	if res.Key.Domain == "" && res.Key.Confidence != key.ConfidenceNone {
		t.Errorf("empty global key with confidence %q", res.Key.Confidence)
	}

	if len(res.Key.Segments) != 2 {
		t.Fatalf("got %d segments, want 2: %+v", len(res.Key.Segments), res.Key.Segments)
	}

	first, second := res.Key.Segments[0], res.Key.Segments[1]
	if first.Domain != "C major" {
		t.Errorf("first segment key = %q, want C major (%+v)", first.Domain, first.Keys)
	}
	if second.Domain != "Eb major" {
		t.Errorf("second segment key = %q, want Eb major (%+v)", second.Domain, second.Keys)
	}
	if math.Abs(first.End-30) > 2 {
		t.Errorf("boundary at %v, want ~30", first.End)
	}
	if first.Frames == 0 || second.Frames == 0 {
		t.Errorf("segments have no frames: %+v", res.Key.Segments)
	}
	if first.Energy <= 0 || second.Energy <= 0 || first.Energy > 1 || second.Energy > 1 {
		t.Errorf("energy out of range: %+v", res.Key.Segments)
	}

	if len(res.Key.Modulations) != 1 {
		t.Fatalf("got %d modulations, want 1: %+v", len(res.Key.Modulations), res.Key.Modulations)
	}
	mod := res.Key.Modulations[0]
	if mod.From != "C major" || mod.To != "Eb major" {
		t.Errorf("modulation = %s -> %s, want C major -> Eb major", mod.From, mod.To)
	}
	if math.Abs(mod.At-30) > 2 {
		t.Errorf("modulation at %v, want ~30", mod.At)
	}
}

func TestAnalyzeSingleKeySong(t *testing.T) {
	pcm := chordHits(44100, 40, 120, 261.63)

	res, err := AnalyzePCM(pcm, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Key.Domain != "C major" {
		t.Errorf("global key = %q, want C major (%+v)", res.Key.Domain, res.Key.Present)
	}
	if len(res.Key.Modulations) != 0 {
		t.Errorf("single-key song reported modulations: %+v", res.Key.Modulations)
	}
	if len(res.Key.Present) == 0 {
		t.Error("no plausible candidates kept for a clear key")
	}
	if res.Key.Present[0].Key != "C major" || res.Key.Present[0].Correlation <= 0 {
		t.Errorf("top candidate = %+v", res.Key.Present[0])
	}
}

func TestAnalyzeSilenceAndShortInput(t *testing.T) {
	res, err := AnalyzePCM(make([]float32, 44100*5), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if res.Tempo.Valid {
		t.Errorf("tempo of silence is valid: %+v", res.Tempo)
	}
	if res.Key.Domain != "" || res.Key.Confidence != key.ConfidenceNone {
		t.Errorf("key of silence = %+v, want none", res.Key)
	}
	if res.Key.Present == nil {
		t.Error("Present must be an empty slice, not nil")
	}

	short, err := AnalyzePCM(make([]float32, 1000), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if short.Tempo.Valid {
		t.Errorf("tempo of a 23 ms clip is valid: %+v", short.Tempo)
	}
	if len(short.Key.Segments) != 0 {
		t.Errorf("segments for a clip shorter than a frame: %+v", short.Key.Segments)
	}
}

func TestAnalyzeRejectsInvalidOptions(t *testing.T) {
	opts := DefaultOptions()
	opts.Rate = 0
	if _, err := AnalyzePCM(nil, opts); err == nil {
		t.Fatal("zero rate accepted")
	}

	opts = DefaultOptions()
	opts.MinBPM = 200
	opts.MaxBPM = 100
	if _, err := AnalyzePCM(nil, opts); err == nil {
		t.Fatal("inverted tempo range accepted")
	}

	opts = DefaultOptions()
	opts.FrameSize = 3000
	if _, err := AnalyzePCM(nil, opts); err == nil {
		t.Fatal("non power-of-two frame size accepted")
	}

	if _, err := Analyze(nil, DefaultOptions()); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestAnalyzeStreamMatchesPCM(t *testing.T) {
	pcm := chordHits(44100, 24, 120, 261.63)

	var buf bytes.Buffer
	for _, v := range pcm {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}

	fromPCM, err := AnalyzePCM(pcm, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	fromStream, err := Analyze(bytes.NewReader(buf.Bytes()), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}

	if fromPCM.Tempo != fromStream.Tempo {
		t.Errorf("tempo differs: %+v vs %+v", fromPCM.Tempo, fromStream.Tempo)
	}
	if fromPCM.Key.Domain != fromStream.Key.Domain {
		t.Errorf("key differs: %q vs %q", fromPCM.Key.Domain, fromStream.Key.Domain)
	}
	if len(fromPCM.Key.Segments) != len(fromStream.Key.Segments) {
		t.Errorf("segments differ: %d vs %d", len(fromPCM.Key.Segments), len(fromStream.Key.Segments))
	}
}

func TestResultJSONShape(t *testing.T) {
	pcm := chordHits(44100, 24, 120, 261.63)
	res, err := AnalyzePCM(pcm, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"duration", "tempo", "key"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing top-level field %q in %s", field, b)
		}
	}

	tempo := raw["tempo"].(map[string]any)
	for _, field := range []string{"bpm", "confidence", "at_range_edge", "valid"} {
		if _, ok := tempo[field]; !ok {
			t.Errorf("missing tempo field %q", field)
		}
	}

	keyObj := raw["key"].(map[string]any)
	// Present must always be an array, never null.
	if _, ok := keyObj["present"].([]any); !ok {
		t.Errorf("key.present = %v, want an array", keyObj["present"])
	}
	segments, ok := keyObj["segments"].([]any)
	if !ok || len(segments) == 0 {
		t.Fatalf("key.segments = %v, want a non-empty array", keyObj["segments"])
	}

	seg := segments[0].(map[string]any)
	for _, field := range []string{"start", "end", "domain", "confidence", "margin", "energy", "frames", "keys"} {
		if _, ok := seg[field]; !ok {
			t.Errorf("missing segment field %q", field)
		}
	}
	if _, ok := seg["keys"].([]any); !ok {
		t.Errorf("segment keys = %v, want an array", seg["keys"])
	}

	// Modulations are omitted when a song does not modulate.
	if _, ok := keyObj["modulations"]; ok {
		t.Errorf("modulations should be omitted for a single-key song: %v", keyObj["modulations"])
	}
}

func TestModulationsRequireConfidenceAndLength(t *testing.T) {
	segments := []KeySegment{
		{Start: 0, End: 30, Domain: "C major", Confidence: key.ConfidenceStrong},
		{Start: 30, End: 35, Domain: "E major", Confidence: key.ConfidenceStrong},    // too short
		{Start: 35, End: 65, Domain: "F major", Confidence: key.ConfidenceAmbiguous}, // ambiguous
		{Start: 65, End: 95, Domain: "G major", Confidence: key.ConfidenceStrong},
	}

	got := modulations(segments, 10)
	if len(got) != 1 {
		t.Fatalf("got %d modulations, want 1: %+v", len(got), got)
	}
	if got[0].From != "C major" || got[0].To != "G major" {
		t.Fatalf("modulation = %s -> %s, want C major -> G major", got[0].From, got[0].To)
	}
	if got[0].At != 65 {
		t.Fatalf("modulation at %v, want 65", got[0].At)
	}
}

func TestAggregateFoldsFramesIntoWindows(t *testing.T) {
	frames := make([]key.Frame, 0, 40)
	for i := 0; i < 40; i++ {
		var c key.Chroma
		c[i%12] = 1
		frames = append(frames, key.Frame{Time: float64(i) * 0.25, RMS: 0.5, Chroma: c})
	}

	points := aggregate(frames, 0.5)
	if len(points) != 20 {
		t.Fatalf("got %d points, want 20", len(points))
	}
	if points[1].Time != 0.5 {
		t.Fatalf("point time = %v, want 0.5", points[1].Time)
	}
	for i, p := range points {
		if len(p.Vector) != 12 {
			t.Fatalf("point %d has %d dimensions", i, len(p.Vector))
		}
		if p.RMS != 0.5 {
			t.Fatalf("point %d RMS = %v, want 0.5", i, p.RMS)
		}
	}
}
