# bpm

Tempo, key and structure analysis for audio, in pure Go.

`bpm` detects the **tempo** of a track, the **global key** with the
Krumhansl-Schmuckler algorithm, and the **local key of every structural
section** — so a song that modulates between keys is reported as a sequence of
sections rather than a single label.

- **Tempo**: Scheirer-style energy + autodifference, with a confidence score
  and an out-of-range indicator.
- **Key**: Krumhansl-Kessler major/minor profiles, Pearson correlation against
  all 24 keys. Every plausible candidate is kept with its correlation, not just
  the winner.
- **Structure**: Foote self-similarity novelty with a Gaussian checkerboard
  kernel, plus heuristic section labels (intro / verse / chorus / bridge /
  outro).
- **Modulations**: key changes between consecutive confident sections, with
  hysteresis so a wobble in correlation is not reported as a key change.
- **Streaming**: the CLI and `song.Analyze` make one pass over the decoded PCM;
  memory scales with the number of chroma frames (megabytes), not with the
  length of the track.

No Python, no PyTorch, no CGO. The only dependencies are pure-Go audio decoders.

## Install

```sh
go install github.com/agberohq/bpm/cmd/bpm@latest
```

Or use it as a library:

```sh
go get github.com/agberohq/bpm
```

## CLI

```sh
bpm track.mp3                 # text report
bpm music/ -workers 4         # recurse, analyse in parallel
bpm -json music/ | jq .       # one JSON object per line
bpm -min-bpm 60 -max-bpm 200 track.flac
```

```text
track.mp3
  duration 1:39.5
  tempo    139.95 BPM (confidence 0.61)
  key      B major (moderate, margin 0.06); also F# minor 0.64
  sections (3):
      0:00.0 -   0:08.5  Intro      B major    strong
      0:08.5 -   1:13.0  Chorus 1   F# minor   ambiguous
      1:13.0 -   1:39.5  Chorus 2   B major    strong
  modulations:
      0:08.5  B major -> F# minor (moderate)
```

Flags: `-json`, `-sections`, `-labels`, `-workers`, `-min-bpm`, `-max-bpm`,
`-top`, `-version`.

Exit codes: `0` success, `1` usage/config error, `2` at least one file failed.

Supported containers: `.mp3`, `.wav`, `.flac`. Audio is downmixed to mono; the
decoder's sample rate is used as-is.

## JSON

```json
{
  "path": "track.mp3",
  "duration": 99.47,
  "tempo": { "bpm": 139.95, "confidence": 0.61, "at_range_edge": false, "valid": true },
  "key": {
    "domain": "B major",
    "confidence": "moderate",
    "margin": 0.061,
    "present": [
      { "key": "B major",  "tonic": "B",  "mode": "major", "correlation": 0.698, "rank": 1 },
      { "key": "F# minor", "tonic": "F#", "mode": "minor", "correlation": 0.637, "rank": 2 }
    ],
    "segments": [
      {
        "start": 8.5, "end": 73.0, "label": "Chorus 1",
        "energy": 1.0, "domain": "F# minor", "confidence": "ambiguous",
        "margin": 0.021, "frames": 1388,
        "keys": [ { "key": "F# minor", "tonic": "F#", "mode": "minor", "correlation": 0.672, "rank": 1 } ]
      }
    ],
    "modulations": [ { "at": 8.5, "from": "B major", "to": "F# minor", "confidence": "moderate" } ]
  }
}
```

`domain` is the dominant key of that scope, `present` holds the other plausible
keys (correlation >= 90% of the best, like the reference implementation), and
`confidence` is derived from the margin between the best and second-best
correlation:

| confidence  | meaning |
|-------------|---------|
| `strong`    | margin >= 0.10 |
| `moderate`  | margin >= 0.03 |
| `ambiguous` | margin below that; treat `present` as a set |
| `none`      | no key passed the absolute correlation floor, or too little audio |

## Library

Whole-song analysis:

```go
// pcmReader is your own adapter from the decoder to mono float32 LE PCM;
// see cmd/bpm/main.go for a working implementation.
res, err := song.Analyze(pcmReader, song.Options{
    Rate:   44100,
    Labels: true,
})
if err != nil {
    return err
}
fmt.Println(res.Key.Domain, res.Key.Present)
for _, seg := range res.Key.Segments {
    fmt.Printf("%6.1fs %-10s %s\n", seg.Start, seg.Label, seg.Domain)
}
```

Tempo only:

```go
p, err := bpm.NewChecked(bpm.WithRate(44100))
if err != nil {
    return err
}

est, err := p.DetectEstimate(r) // r is mono float32 LE PCM
if errors.Is(err, bpm.ErrNoResult) {
    // silence, a very short clip, or material with no rhythm
}
fmt.Println(est.BPM, est.Confidence)
```

Key only:

```go
ex := key.NewExtractor(44100)
frames, err := ex.Extract(pcm)
var chroma key.Chroma
for _, f := range frames {
    chroma = chroma.Add(f.Chroma)
}

a := key.NewCorrelator(key.Options{IncludeAll: true}).Analyze(chroma, len(frames))
for _, c := range a.All[:5] {
    fmt.Printf("%-10s %.3f\n", c.Key, c.Correlation)
}
```

Segments only:

```go
spans := segment.Detect(points, duration, segment.Options{
    KernelSeconds:     4,
    MinSegmentSeconds: 8,
})
```

## How it works

**Tempo.** PCM is reduced to an energy envelope (attack/release smoothing, 128
samples per value). For each candidate interval the weighted autodifference of
the envelope at beat lags is compared with half-beat lags; the interval with the
lowest average wins. Silence, clips shorter than two beats, and material without
at least four onsets are rejected with `ErrNoResult` instead of returning a
confident number.

**Key.** Frames of 8192 samples (Hann window, hop 4096) are transformed with a
radix-2 FFT. Spectral magnitude is folded into twelve pitch classes using
interpolated semitone mapping — nearest-bin rounding loses bass notes, because
below ~200 Hz an FFT bin is wider than a semitone. The chroma of a passage is
correlated (Pearson) with the Krumhansl-Kessler profiles rotated to all twelve
tonics.

**Structure.** Chroma frames are aggregated into 0.5 s points and compared with
a mean-centred cosine (correlation), mixed with a small loudness term. A
Gaussian checkerboard kernel on that self-similarity matrix produces a novelty
curve; peaks above mean + k·σ become boundaries, subject to a minimum segment
length. Each boundary is then verified: the material before and after must
differ in harmony or loudness, which keeps homogeneous loops from being chopped
into noise. This is the classical self-similarity approach that deep models such
as All-In-One build on.

**Modulations.** Only sections lasting at least `MinModulationSeconds` and whose
key is `strong` or `moderate` can start a modulation. Ambiguous sections are
skipped, not treated as a change.

## Performance

Measured on an Apple M-series laptop, 44.1 kHz mono:

| workload | time | memory |
|---|---|---|
| chroma extraction | ~950x realtime | — |
| full analysis (`song.AnalyzePCM`) | ~400x realtime | 6 MB per 30 s clip |
| MP3 CLI, 99 s track | 1.2 s | 12 MB peak RSS |
| WAV CLI, 10 min track | 1.6 s | 19 MB peak RSS |

Memory is independent of track length beyond the retained chroma frames: about
112 bytes per frame, one frame per 93 ms (~4 MB per hour of audio).

## Limitations

- **Functional labels are heuristic.** Intro/outro come from position, length
  and loudness; chorus is the most frequent loud part; a unique section between
  choruses is a bridge. Instrumentation ("inst", "solo") cannot be inferred from
  chroma and is not reported. Plug in a trained model via
  `song.Options.Labeller` (the `segment.Labeller` interface) for
  production-grade labelling.
- **Key detection uses STFT chroma**, an approximation of librosa's
  `chroma_cqt`. There is no harmonic/percussive separation, so percussive
  material dilutes the chroma; `Options.Whiten` reduces that.
- **Triads are ambiguous.** Relative major/minor pairs share all seven notes;
  that is why `present` is a list and why `confidence` exists. Do not treat
  `domain` as ground truth when `confidence` is `ambiguous`.
- **Tempo octave errors are inherent** to the method: a 170 BPM track inside an
  84-146 range may land on half or two-thirds tempo. Widen `MinBPM`/`MaxBPM`
  for EDM and classical music.
- **MP3 decoding has no error reporting** in the decoder used, so a file that is
  corrupt mid-stream looks like a shorter file. Prefer WAV for archival
  analysis, as All-In-One's authors also recommend.
- **Not concurrent-safe per Processor.** Create one `bpm.Processor` per
  goroutine; `song.Analyze` is safe to call concurrently with separate options.

## References

- Krumhansl, C. L. & Kessler, E. J. (1982). Tracing the dynamic changes in
  perceived tonal organization in a spatial representation of musical keys.
- Foote, J. (2000). Automatic audio segmentation using a measure of audio
  novelty.
- Kim, T. & Nam, J. (2023). All-In-One Metrical and Functional Structure
  Analysis with Neighborhood Attentions on Demixed Audio (WASPAA).
- jackmcarthur/musical-key-finder — the Krumhansl-Schmuckler reference used for
  the profiles and the 0.9 relative-candidate rule.

## License

MIT.


