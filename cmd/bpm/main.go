// Command bpm reports the tempo, key and structure of audio files.
//
// It reads mono float32 PCM from decoded audio and analyses each file in a
// single streaming pass, so memory use is independent of track length.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/agberohq/bpm"
	"github.com/agberohq/bpm/key"
	"github.com/agberohq/bpm/song"
	"github.com/gopxl/beep"
	"github.com/gopxl/beep/flac"
	"github.com/gopxl/beep/mp3"
	"github.com/gopxl/beep/wav"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	json     bool
	sections bool
	labels   bool
	workers  int
	minBPM   float64
	maxBPM   float64
	top      int
	version  bool
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, paths, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "bpm: %v\n", err)
		return 1
	}
	if opts.version {
		fmt.Fprintf(stdout, "bpm %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return 0
	}
	if len(paths) == 0 {
		printUsage(stderr)
		return 1
	}

	files, err := collectAudioFiles(paths, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "bpm: %v\n", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "bpm: no supported audio files found (want .mp3, .wav or .flac)")
		return 2
	}

	cfg, err := analysisOptions(opts)
	if err != nil {
		fmt.Fprintf(stderr, "bpm: %v\n", err)
		return 1
	}

	results := analyzeAll(files, cfg, opts.workers)

	failed := 0
	for _, res := range results {
		if res.err != nil {
			failed++
			if opts.json {
				writeJSONError(stdout, res.path, res.err)
			} else {
				fmt.Fprintf(stderr, "%s: %v\n", res.path, res.err)
			}
			continue
		}
		if opts.json {
			writeJSON(stdout, res.result)
		} else {
			writeText(stdout, res.result, opts.sections)
		}
	}

	if failed > 0 {
		if !opts.json {
			fmt.Fprintf(stderr, "bpm: %d of %d files failed\n", failed, len(results))
		}
		return 2
	}
	return 0
}

func parseFlags(args []string, stderr io.Writer) (options, []string, error) {
	var opts options

	fs := flag.NewFlagSet("bpm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { printUsage(stderr) }

	fs.BoolVar(&opts.json, "json", false, "emit one JSON object per file (NDJSON)")
	fs.BoolVar(&opts.sections, "sections", true, "print the per-section table")
	fs.BoolVar(&opts.labels, "labels", true, "label sections heuristically (intro/verse/chorus/...)")
	fs.IntVar(&opts.workers, "workers", defaultWorkers(), "number of files analysed concurrently")
	fs.Float64Var(&opts.minBPM, "min-bpm", 84, "slowest tempo considered")
	fs.Float64Var(&opts.maxBPM, "max-bpm", 146, "fastest tempo considered")
	fs.IntVar(&opts.top, "top", 4, "number of key candidates to keep per analysis")
	fs.BoolVar(&opts.version, "version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		return opts, nil, err
	}
	if opts.workers < 1 {
		return opts, nil, fmt.Errorf("-workers must be at least 1, got %d", opts.workers)
	}
	if opts.minBPM <= 0 || opts.maxBPM <= 0 || opts.minBPM >= opts.maxBPM {
		return opts, nil, fmt.Errorf("invalid tempo range %g..%g", opts.minBPM, opts.maxBPM)
	}
	if opts.top < 1 {
		return opts, nil, fmt.Errorf("-top must be at least 1, got %d", opts.top)
	}
	return opts, fs.Args(), nil
}

func defaultWorkers() int {
	n := runtime.NumCPU()
	if n > 4 {
		n = 4
	}
	if n < 1 {
		n = 1
	}
	return n
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: bpm [flags] <file-or-directory>...

Reports tempo, global key, per-section key analysis and modulations for
.mp3, .wav and .flac files. Directories are walked recursively.

Flags:
  -json          emit one JSON object per file (NDJSON)
  -sections      print the per-section table (default true)
  -labels        label sections heuristically (default true)
  -workers N     files analysed concurrently (default: min(NumCPU, 4))
  -min-bpm BPM   slowest tempo considered (default 84)
  -max-bpm BPM   fastest tempo considered (default 146)
  -top N         key candidates kept per analysis (default 4)
  -version       print the version and exit

Exit codes: 0 success, 1 usage error, 2 at least one file failed.
`)
}

func analysisOptions(opts options) (song.Options, error) {
	cfg := song.DefaultOptions()
	cfg.MinBPM = bpm.BPM(opts.minBPM)
	cfg.MaxBPM = bpm.BPM(opts.maxBPM)
	cfg.Key.MaxCandidates = opts.top
	cfg.Labels = opts.labels
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func collectAudioFiles(paths []string, stderr io.Writer) ([]string, error) {
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", path, err)
			continue
		}
		if !info.IsDir() {
			if isAudio(path) {
				files = append(files, path)
			} else {
				fmt.Fprintf(stderr, "%s: unsupported extension (want .mp3, .wav or .flac)\n", path)
			}
			continue
		}
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", p, err)
				return nil
			}
			if d.IsDir() || !isAudio(p) {
				return nil
			}
			files = append(files, p)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func isAudio(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".wav", ".flac":
		return true
	}
	return false
}

type fileResult struct {
	path   string
	result *song.Result
	err    error
}

// analyzeAll processes files with a bounded worker pool. Results are stored by
// input index so output order stays deterministic regardless of scheduling.
func analyzeAll(files []string, cfg song.Options, workers int) []fileResult {
	results := make([]fileResult, len(files))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				results[idx] = analyzeFile(files[idx], cfg)
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func analyzeFile(path string, cfg song.Options) fileResult {
	res := fileResult{path: path}

	reader, closeFn, rate, err := openAudio(path)
	if err != nil {
		res.err = err
		return res
	}
	defer closeFn()

	cfg.Rate = rate
	result, err := song.Analyze(reader, cfg)
	if err != nil {
		res.err = err
		return res
	}
	result.Path = path
	res.result = result
	return res
}

// openAudio decodes the container and returns a reader of mono float32
// little-endian PCM plus the source sample rate.
func openAudio(path string) (io.Reader, func(), float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}

	var streamer beep.StreamSeekCloser
	var format beep.Format

	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		streamer, format, err = mp3.Decode(f)
	case ".wav":
		streamer, format, err = wav.Decode(f)
	case ".flac":
		streamer, format, err = flac.Decode(f)
	default:
		f.Close()
		return nil, nil, 0, fmt.Errorf("unsupported audio format (want .mp3, .wav or .flac)")
	}
	if err != nil {
		f.Close()
		return nil, nil, 0, fmt.Errorf("decode: %w", err)
	}

	pr := &pcmReader{
		streamer: streamer,
		frames:   make([][2]float64, 4096),
	}
	closeFn := func() {
		streamer.Close()
		f.Close()
	}
	return pr, closeFn, float64(format.SampleRate), nil
}

// pcmReader downmixes a beep streamer to mono and encodes it as float32 LE,
// the format the analysis packages consume. It streams: only one block of
// frames and any partial output buffer are held in memory.
type pcmReader struct {
	streamer beep.StreamSeekCloser
	frames   [][2]float64
	pending  []byte
	done     bool
}

func (r *pcmReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) < len(p) && !r.done {
		n, ok := r.streamer.Stream(r.frames)
		for i := 0; i < n; i++ {
			mono := float32((r.frames[i][0] + r.frames[i][1]) / 2)
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(mono))
			r.pending = append(r.pending, b[:]...)
		}
		if !ok {
			r.done = true
		}
	}

	if len(r.pending) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func writeJSON(w io.Writer, res *song.Result) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(res)
}

func writeJSONError(w io.Writer, path string, err error) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	}{path, err.Error()})
}

func writeText(w io.Writer, res *song.Result, sections bool) {
	fmt.Fprintf(w, "%s\n", res.Path)
	fmt.Fprintf(w, "  duration %s\n", clock(res.Duration))

	if res.Tempo.Valid {
		edge := ""
		if res.Tempo.AtRangeEdge {
			edge = ", at range edge"
		}
		fmt.Fprintf(w, "  tempo    %.2f BPM (confidence %.2f%s)\n", res.Tempo.BPM, res.Tempo.Confidence, edge)
	} else {
		fmt.Fprintln(w, "  tempo    not detected")
	}

	if res.Key.Domain != "" {
		fmt.Fprintf(w, "  key      %s (%s, margin %.2f)", res.Key.Domain, res.Key.Confidence, res.Key.Margin)
		if alts := alternatives(res.Key.Present); alts != "" {
			fmt.Fprintf(w, "; also %s", alts)
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintf(w, "  key      not determined (%s)\n", res.Key.Confidence)
	}

	if sections && len(res.Key.Segments) > 0 {
		fmt.Fprintf(w, "  sections (%d):\n", len(res.Key.Segments))
		for _, s := range res.Key.Segments {
			fmt.Fprintf(w, "    %8s - %8s  %-10s %-10s %s\n",
				clock(s.Start), clock(s.End), dash(s.Label), dash(s.Domain), s.Confidence)
		}
	}

	if len(res.Key.Modulations) > 0 {
		fmt.Fprintln(w, "  modulations:")
		for _, m := range res.Key.Modulations {
			fmt.Fprintf(w, "    %8s  %s -> %s (%s)\n", clock(m.At), m.From, m.To, m.Confidence)
		}
	}
	fmt.Fprintln(w)
}

func alternatives(candidates []key.Candidate) string {
	if len(candidates) < 2 {
		return ""
	}
	var parts []string
	for _, c := range candidates[1:] {
		if len(parts) == 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %.2f", c.Key, c.Correlation))
	}
	return strings.Join(parts, ", ")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// clock formats seconds as m:ss.s.
func clock(seconds float64) string {
	if seconds < 0 || math.IsNaN(seconds) {
		seconds = 0
	}
	minutes := int(seconds) / 60
	rest := seconds - float64(minutes*60)
	return fmt.Sprintf("%d:%04.1f", minutes, rest)
}
