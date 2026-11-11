package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// writeWAV writes a 16-bit mono PCM WAV containing chord hits at the given
// tempo, which gives both tonal content and rhythmic attacks.
func writeWAV(t *testing.T, path string, bpm, root float64) {
	t.Helper()

	const rate = 44100
	n := rate * 12
	samples := make([]int16, n)
	period := int(math.Round(float64(rate) * 60 / bpm))
	freqs := []float64{root / 2, root, root * 1.25, root * 1.5}

	for beat := 0; beat*period < n; beat++ {
		start := beat * period
		for i := 0; i < rate/3 && start+i < n; i++ {
			env := math.Exp(-float64(i) / (0.12 * rate))
			var s float64
			for _, f := range freqs {
				s += 0.12 * math.Sin(2*math.Pi*f*float64(i)/rate)
			}
			samples[start+i] += int16(env * s * 20000)
		}
	}

	data := make([]byte, len(samples)*2)
	for i, v := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(v))
	}

	hdr := make([]byte, 44)
	copy(hdr, "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(36+len(data)))
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1)
	binary.LittleEndian.PutUint16(hdr[22:], 1)
	binary.LittleEndian.PutUint32(hdr[24:], rate)
	binary.LittleEndian.PutUint32(hdr[28:], rate*2)
	binary.LittleEndian.PutUint16(hdr[32:], 2)
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(data)))

	if err := os.WriteFile(path, append(hdr, data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunTextOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	writeWAV(t, path, 120, 261.63)

	var stdout, stderr bytes.Buffer
	code := run([]string{path}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{path, "BPM", "key", "sections"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	m := regexp.MustCompile(`([0-9]+\.[0-9]+) BPM`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no tempo in output:\n%s", out)
	}
	bpm, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(bpm-120) > 1 {
		t.Errorf("tempo = %v, want ~120", bpm)
	}
}

func TestRunJSONOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.wav")
	writeWAV(t, path, 100, 220.0)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-json", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	var res struct {
		Path  string `json:"path"`
		Tempo struct {
			BPM   float64 `json:"bpm"`
			Valid bool    `json:"valid"`
		} `json:"tempo"`
		Key struct {
			Domain   string `json:"domain"`
			Segments []struct {
				Start float64 `json:"start"`
				End   float64 `json:"end"`
				Keys  []struct {
					Key         string  `json:"key"`
					Correlation float64 `json:"correlation"`
				} `json:"keys"`
			} `json:"segments"`
		} `json:"key"`
	}
	line := strings.TrimSpace(stdout.String())
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("expected one JSON object per line, got:\n%s", line)
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, line)
	}
	if res.Path != path {
		t.Errorf("path = %q, want %q", res.Path, path)
	}
	if !res.Tempo.Valid || math.Abs(res.Tempo.BPM-100) > 1 {
		t.Errorf("tempo = %+v, want ~100", res.Tempo)
	}
	if len(res.Key.Segments) == 0 {
		t.Fatal("no segments in JSON output")
	}
	if len(res.Key.Segments[0].Keys) == 0 {
		t.Fatal("no key candidates in the first segment")
	}
}

func TestRunExitCodes(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(bad, []byte("not a wav"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{bad}, &stdout, &stderr); code != 2 {
		t.Errorf("exit = %d for a corrupt file, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bad.wav") {
		t.Errorf("stderr does not mention the file: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Errorf("exit = %d with no arguments, want 1", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("no usage message: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-version"}, &stdout, &stderr); code != 0 {
		t.Errorf("exit = %d for -version, want 0", code)
	}
	if !strings.Contains(stdout.String(), "bpm ") {
		t.Errorf("version output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-min-bpm", "200", "-max-bpm", "100", bad}, &stdout, &stderr); code != 1 {
		t.Errorf("exit = %d for an invalid tempo range, want 1", code)
	}
}

func TestRunDirectoryAndWorkers(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"b.wav", "a.wav", "notes.txt"} {
		path := filepath.Join(dir, name)
		if name == "notes.txt" {
			if err := os.WriteFile(path, []byte("ignore me"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeWAV(t, path, 120, 261.63+float64(i))
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-json", "-workers", "2", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d JSON lines, want 2:\n%s", len(lines), stdout.String())
	}
	// Results are emitted in sorted path order regardless of worker scheduling.
	if !strings.Contains(lines[0], "a.wav") || !strings.Contains(lines[1], "b.wav") {
		t.Fatalf("output not sorted:\n%s", stdout.String())
	}
}

func TestPCMReaderStreamsAndDownmixes(t *testing.T) {
	src := &fakeStreamer{frames: [][2]float64{{1, 1}, {0.5, 0.5}, {-1, -1}}}
	r := &pcmReader{streamer: src, frames: make([][2]float64, 4)}

	buf := make([]byte, 12)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 12 {
		t.Fatalf("read %d bytes, want 12", n)
	}
	for i, want := range []float32{1, 0.5, -1} {
		got := math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
		if math.Abs(float64(got-want)) > 1e-6 {
			t.Fatalf("sample %d = %v, want %v", i, got, want)
		}
	}

	if _, err := r.Read(buf); err == nil {
		t.Fatal("expected EOF at the end of the stream")
	}
}

type fakeStreamer struct {
	frames [][2]float64
	pos    int
}

func (s *fakeStreamer) Stream(buf [][2]float64) (int, bool) {
	if s.pos >= len(s.frames) {
		return 0, false
	}
	n := copy(buf, s.frames[s.pos:])
	s.pos += n
	return n, s.pos < len(s.frames)
}

func (s *fakeStreamer) Err() error    { return nil }
func (s *fakeStreamer) Len() int      { return len(s.frames) }
func (s *fakeStreamer) Position() int { return s.pos }
func (s *fakeStreamer) Seek(p int) error {
	s.pos = p
	return nil
}
func (s *fakeStreamer) Close() error { return nil }
