// Package segment divides a song into structural sections.
//
// Boundaries are found with Foote's checkerboard kernel applied to a
// self-similarity matrix of chroma vectors. This is the classical
// self-similarity approach that deep models such as All-In-One (Kim & Nam,
// 2023) build on: sections are regions where the music repeats, and boundaries
// are the diagonals where the "past" and "future" texture diverge.
//
// The package is deliberately model-free: it needs no training data and no
// Python runtime. Functional labels (intro/verse/chorus/...) are heuristic;
// supply a trained Labeller for production-grade labelling.
package segment

import (
	"fmt"
	"math"
	"sort"
)

// Point is one analysis frame: a feature vector (normally chroma) and its
// loudness.
type Point struct {
	Time   float64
	Vector []float64
	RMS    float64
}

// Options controls boundary detection.
type Options struct {
	// WindowSeconds is the spacing of Points; it is read from the Point times,
	// but is used as a fallback when only one point is supplied.
	WindowSeconds float64
	// KernelSeconds is the half-width of the checkerboard kernel. Larger
	// values find broader sections, smaller values find finer ones.
	KernelSeconds float64
	// MinSegmentSeconds is the shortest section the detector will produce.
	MinSegmentSeconds float64
	// ThresholdK sets the peak threshold in standard deviations above the mean
	// novelty. Higher values yield fewer boundaries.
	ThresholdK float64
	// MinNovelty is an absolute floor for boundary strength; it prevents
	// spurious boundaries in very homogeneous music.
	MinNovelty float64
	// MaxPoints caps the self-similarity matrix size. Points are averaged
	// pairwise until the limit is met, which bounds memory at
	// MaxPoints^2 * 4 bytes.
	MaxPoints int
	// MaxBoundarySimilarity rejects a boundary whose two sides are more alike
	// than this (mean-centred cosine, i.e. correlation). It is what keeps a
	// homogeneous loop from being chopped into noise. Values >= 1 disable the
	// check.
	MaxBoundarySimilarity float64
	// MinBoundaryEnergyChange accepts a boundary whose two sides differ in
	// loudness by at least this fraction, even when the harmony is similar.
	// Negative values disable the check.
	MinBoundaryEnergyChange float64
	// EnergyWeight mixes loudness into the self-similarity matrix, in [0, 1].
	// Verse and chorus often share harmony and differ mainly in energy, so a
	// small weight finds boundaries that chroma alone misses. Negative values
	// disable the mix.
	EnergyWeight float64
}

// DefaultOptions returns a configuration suited to 3-4 minute songs.
func DefaultOptions() Options {
	return Options{
		WindowSeconds:           0.5,
		KernelSeconds:           4,
		MinSegmentSeconds:       8,
		ThresholdK:              1.0,
		MinNovelty:              0,
		MaxPoints:               1200,
		MaxBoundarySimilarity:   0.9,
		MinBoundaryEnergyChange: 0.25,
		EnergyWeight:            0.2,
	}
}

// WithDefaults returns o with zero-valued fields replaced by defaults.
func (o Options) WithDefaults() Options {
	d := DefaultOptions()
	if !(o.WindowSeconds > 0) {
		o.WindowSeconds = d.WindowSeconds
	}
	if !(o.KernelSeconds > 0) {
		o.KernelSeconds = d.KernelSeconds
	}
	if !(o.MinSegmentSeconds > 0) {
		o.MinSegmentSeconds = d.MinSegmentSeconds
	}
	if o.ThresholdK == 0 {
		o.ThresholdK = d.ThresholdK
	}
	if o.MaxPoints <= 0 {
		o.MaxPoints = d.MaxPoints
	}
	if o.MaxBoundarySimilarity == 0 {
		o.MaxBoundarySimilarity = d.MaxBoundarySimilarity
	}
	if o.MinBoundaryEnergyChange == 0 {
		o.MinBoundaryEnergyChange = d.MinBoundaryEnergyChange
	}
	if o.EnergyWeight == 0 {
		o.EnergyWeight = d.EnergyWeight
	}
	return o
}

// Validate checks the configuration.
func (o Options) Validate() error {
	switch {
	case !(o.WindowSeconds > 0):
		return fmt.Errorf("segment: window must be > 0, got %v", o.WindowSeconds)
	case !(o.KernelSeconds > 0):
		return fmt.Errorf("segment: kernel must be > 0, got %v", o.KernelSeconds)
	case !(o.MinSegmentSeconds > 0):
		return fmt.Errorf("segment: minimum segment must be > 0, got %v", o.MinSegmentSeconds)
	case o.MaxPoints < 8:
		return fmt.Errorf("segment: max points must be >= 8, got %d", o.MaxPoints)
	}
	if o.MinSegmentSeconds < o.WindowSeconds {
		return fmt.Errorf("segment: minimum segment %v is shorter than the analysis window %v", o.MinSegmentSeconds, o.WindowSeconds)
	}
	return nil
}

// Boundary is a detected section change.
type Boundary struct {
	Time     float64 `json:"time"`
	Strength float64 `json:"strength"`
}

// Span is a section of the song.
type Span struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Duration returns the span length in seconds.
func (s Span) Duration() float64 { return s.End - s.Start }

// Detect returns the structural sections of a song.
//
// duration is the total length of the audio; when it is not positive the end
// of the final point is used. The result always covers [0, duration] and
// contains at least one span when there is any input.
//
// Detect returns nil when the options are invalid; call Options.Validate to
// tell that apart from an empty input.
func Detect(points []Point, duration float64, opts Options) []Span {
	o := opts.WithDefaults()
	if o.Validate() != nil || len(points) == 0 {
		return nil
	}
	if len(points) == 1 {
		d := duration
		if !(d > 0) {
			d = points[0].Time + o.WindowSeconds
		}
		return []Span{{Start: 0, End: d}}
	}

	if duration <= 0 {
		duration = points[len(points)-1].Time + medianStep(points)
	}

	bounds := Boundaries(points, opts)
	kept := make([]Boundary, 0, len(bounds))
	for _, b := range bounds {
		if b.Time <= 0 || b.Time >= duration {
			continue
		}
		kept = append(kept, b)
	}
	return spans(kept, duration, o)
}

// Boundaries returns the detected section changes with their novelty strength.
//
// This is the raw structural signal behind Detect: useful for diagnostics, or
// when a caller wants to apply its own segmentation policy.
func Boundaries(points []Point, opts Options) []Boundary {
	o := opts.WithDefaults()
	if o.Validate() != nil || len(points) < 3 {
		return nil
	}

	pts := downsample(points, o.MaxPoints)
	step := medianStep(pts)
	radius := kernelRadius(len(pts), step, o)
	if radius < 1 {
		return nil
	}

	nov := Novelty(pts, o)
	return verifyBoundaries(pts, pickPeaks(nov, step, radius, o), step, radius, o)
}

// verifyBoundaries drops novelty peaks that do not correspond to an actual
// change of musical content.
//
// A novelty peak says "something happened here"; it does not say the two sides
// differ. In homogeneous material (a loop, a sustained drone, broadband
// percussion) the self-similarity matrix is noisy and peaks appear everywhere.
// Requiring either a harmonic or a loudness difference removes those.
func verifyBoundaries(points []Point, bounds []Boundary, step float64, radius int, o Options) []Boundary {
	if len(bounds) == 0 || radius < 1 || !(step > 0) {
		return bounds
	}
	out := make([]Boundary, 0, len(bounds))
	for _, b := range bounds {
		i := int(math.Round(b.Time / step))
		if i-radius < 0 || i+radius > len(points) {
			out = append(out, b)
			continue
		}

		left, leftRMS := meanVector(points[i-radius : i])
		right, rightRMS := meanVector(points[i : i+radius])

		changed := true
		if o.MaxBoundarySimilarity < 1 {
			changed = similarity(left, right) < o.MaxBoundarySimilarity
		}
		if !changed && o.MinBoundaryEnergyChange > 0 {
			maxRMS := math.Max(leftRMS, rightRMS)
			changed = maxRMS > 0 && math.Abs(leftRMS-rightRMS)/maxRMS >= o.MinBoundaryEnergyChange
		}
		if changed {
			out = append(out, b)
		}
	}
	return out
}

func meanVector(points []Point) ([]float64, float64) {
	if len(points) == 0 {
		return nil, 0
	}
	out := make([]float64, len(points[0].Vector))
	var rms float64
	for _, p := range points {
		for i := range out {
			if i < len(p.Vector) {
				out[i] += p.Vector[i]
			}
		}
		rms += p.RMS
	}
	n := float64(len(points))
	for i := range out {
		out[i] /= n
	}
	return out, rms / n
}

// Novelty returns the checkerboard novelty curve for points. Values are
// normalised by the kernel weight, so they are comparable across window sizes.
func Novelty(points []Point, opts Options) []float64 {
	o := opts.WithDefaults()
	n := len(points)
	if n < 3 {
		return nil
	}

	step := medianStep(points)
	if !(step > 0) {
		return nil
	}
	radius := kernelRadius(n, step, o)
	if radius < 1 {
		return nil
	}

	sim := make([]float32, n*n)
	weight := o.EnergyWeight
	if weight > 1 {
		weight = 1
	}
	for i := 0; i < n; i++ {
		sim[i*n+i] = 1
		for j := i + 1; j < n; j++ {
			s := similarity(points[i].Vector, points[j].Vector)
			if weight > 0 {
				e := energySimilarity(points[i].RMS, points[j].RMS)
				s = (1-weight)*s + weight*e
			}
			sim[i*n+j] = float32(s)
			sim[j*n+i] = float32(s)
		}
	}

	sigma := float64(radius) / 2
	kernel := make([]float64, 2*radius+1)
	for u := -radius; u <= radius; u++ {
		kernel[u+radius] = math.Exp(-float64(u*u) / (2 * sigma * sigma))
	}

	nov := make([]float64, n)
	for i := radius; i < n-radius; i++ {
		var acc, norm float64
		for u := -radius; u <= radius; u++ {
			iu := i + u
			if iu < 0 || iu >= n {
				continue
			}
			row := sim[iu*n : iu*n+n]
			wu := kernel[u+radius]
			for v := -radius; v <= radius; v++ {
				iv := i + v
				if iv < 0 || iv >= n {
					continue
				}
				w := wu * kernel[v+radius]
				// Gaussian checkerboard: positive on the same-side
				// quadrants, negative across the boundary diagonal.
				sign := 1.0
				if (u < 0) != (v < 0) {
					sign = -1
				}
				acc += sign * w * float64(row[iv])
				norm += w
			}
		}
		if norm > 0 {
			nov[i] = acc / norm
		}
	}
	return nov
}

func pickPeaks(nov []float64, step float64, radius int, o Options) []Boundary {
	lo := radius
	hi := len(nov) - radius
	if radius < 1 || hi-lo < 3 {
		return nil
	}

	// Statistics are taken over the interior only: the kernel is truncated
	// near the edges, so those values are not comparable.
	var sum, sumsq float64
	for i := lo; i < hi; i++ {
		sum += nov[i]
		sumsq += nov[i] * nov[i]
	}
	n := float64(hi - lo)
	mean := sum / n
	variance := sumsq/n - mean*mean
	if variance <= 0 {
		// A flat novelty curve has no structure to report.
		return nil
	}
	threshold := mean + o.ThresholdK*math.Sqrt(variance)
	if threshold < o.MinNovelty {
		threshold = o.MinNovelty
	}

	var out []Boundary
	for i := lo; i < hi; i++ {
		v := nov[i]
		if v <= threshold {
			continue
		}
		if v <= nov[i-1] || v < nov[i+1] {
			continue
		}
		t := float64(i) * step
		if len(out) > 0 {
			last := &out[len(out)-1]
			if t-last.Time < o.MinSegmentSeconds {
				if v > last.Strength {
					last.Time = t
					last.Strength = v
				}
				continue
			}
		}
		out = append(out, Boundary{Time: t, Strength: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

func kernelRadius(n int, step float64, o Options) int {
	if !(step > 0) || n < 3 {
		return 0
	}
	r := int(math.Round(o.KernelSeconds / step))
	if r < 1 {
		r = 1
	}
	if r > n/2 {
		r = n / 2
	}
	return r
}

func spans(bounds []Boundary, duration float64, o Options) []Span {
	out := make([]Span, 0, len(bounds)+1)
	start := 0.0
	for _, b := range bounds {
		if b.Time-start < o.MinSegmentSeconds {
			continue
		}
		if duration-b.Time < o.MinSegmentSeconds {
			break
		}
		out = append(out, Span{Start: start, End: b.Time})
		start = b.Time
	}
	out = append(out, Span{Start: start, End: duration})
	return out
}

func downsample(points []Point, max int) []Point {
	for len(points) > max {
		next := make([]Point, 0, (len(points)+1)/2)
		for i := 0; i < len(points); i += 2 {
			if i+1 >= len(points) {
				next = append(next, points[i])
				continue
			}
			next = append(next, mergePoints(points[i], points[i+1]))
		}
		points = next
	}
	return points
}

func mergePoints(a, b Point) Point {
	v := make([]float64, len(a.Vector))
	for i := range v {
		if i < len(b.Vector) {
			v[i] = (a.Vector[i] + b.Vector[i]) / 2
		} else {
			v[i] = a.Vector[i]
		}
	}
	return Point{
		Time:   a.Time,
		Vector: l2(v),
		RMS:    (a.RMS + b.RMS) / 2,
	}
}

// similarity is the mean-centred cosine (Pearson correlation) of two feature
// vectors.
//
// Chroma vectors are non-negative, so their plain cosine similarity is high
// even for unrelated harmony. Centring first makes the self-similarity matrix
// discriminate between sections, which is what the checkerboard kernel needs.
func similarity(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}

	var ma, mb float64
	for i := 0; i < n; i++ {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(n)
	mb /= float64(n)

	var dot, na, nb float64
	for i := 0; i < n; i++ {
		x := a[i] - ma
		y := b[i] - mb
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na <= 0 || nb <= 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

func l2(v []float64) []float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if sum <= 0 {
		return v
	}
	norm := math.Sqrt(sum)
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

// energySimilarity compares two loudness values on a scale-free 0..1 axis.
func energySimilarity(a, b float64) float64 {
	max := math.Max(a, b)
	if max <= 0 {
		return 1
	}
	return 1 - math.Abs(a-b)/max
}

func medianStep(points []Point) float64 {
	if len(points) < 2 {
		return 0
	}
	steps := make([]float64, 0, len(points)-1)
	for i := 1; i < len(points); i++ {
		d := points[i].Time - points[i-1].Time
		if d > 0 {
			steps = append(steps, d)
		}
	}
	if len(steps) == 0 {
		return 0
	}
	sort.Float64s(steps)
	return steps[len(steps)/2]
}
