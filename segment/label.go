package segment

import "fmt"

// Section is a span together with the aggregated features used for labelling.
type Section struct {
	Start  float64
	End    float64
	Vector []float64
	RMS    float64
}

// Duration returns the section length in seconds.
func (s Section) Duration() float64 { return s.End - s.Start }

// Labeller assigns functional labels to sections. Implement it to plug in a
// trained model (for example All-In-One exported to ONNX) in place of the
// heuristic labeller.
type Labeller interface {
	Labels(sections []Section) []string
}

// HeuristicOptions tunes the heuristic labeller.
type HeuristicOptions struct {
	// SimilarityThreshold is the cosine similarity above which two sections
	// are considered the same musical part.
	SimilarityThreshold float64
	// IntroMaxFraction labels an opening section as "Intro" when it is shorter
	// than this fraction of the song.
	IntroMaxFraction float64
	// QuietRatio labels an opening or closing section as "Intro"/"Outro" when
	// it is quieter than this fraction of the mean section loudness.
	QuietRatio float64
}

// DefaultHeuristicOptions returns the default labeller configuration.
func DefaultHeuristicOptions() HeuristicOptions {
	return HeuristicOptions{
		SimilarityThreshold: 0.9,
		IntroMaxFraction:    0.2,
		QuietRatio:          0.8,
	}
}

func (o HeuristicOptions) withDefaults() HeuristicOptions {
	d := DefaultHeuristicOptions()
	if !(o.SimilarityThreshold > 0) {
		o.SimilarityThreshold = d.SimilarityThreshold
	}
	if !(o.IntroMaxFraction > 0) {
		o.IntroMaxFraction = d.IntroMaxFraction
	}
	if !(o.QuietRatio > 0) {
		o.QuietRatio = d.QuietRatio
	}
	return o
}

// HeuristicLabeller labels sections using repetition and loudness.
//
// It can identify the structural roles that follow from repetition alone:
// a recurring loud part becomes "Chorus", other recurring parts become
// "Verse N", a short or quiet opening/closing part becomes "Intro"/"Outro",
// and a single occurrence between two choruses becomes "Bridge". Anything it
// cannot infer is left as "Section N".
//
// It cannot infer instrumentation (All-In-One's "inst"/"solo") or semantic
// roles that are not visible in chroma and loudness. Use a trained Labeller
// when that matters.
type HeuristicLabeller struct {
	Options HeuristicOptions
}

// Labels implements Labeller.
func (l HeuristicLabeller) Labels(sections []Section) []string {
	return HeuristicLabels(sections, l.Options)
}

// HeuristicLabels is the function form of HeuristicLabeller.
func HeuristicLabels(sections []Section, opts HeuristicOptions) []string {
	o := opts.withDefaults()
	n := len(sections)
	labels := make([]string, n)
	if n == 0 {
		return labels
	}
	if n == 1 {
		labels[0] = "Section 1"
		return labels
	}

	assign, counts, meanRMS := clusterSections(sections, o.SimilarityThreshold)

	var total, totalRMS float64
	for _, s := range sections {
		total += s.Duration()
		totalRMS += s.RMS
	}
	totalRMS /= float64(n)

	labelled := make([]bool, n)
	isQuietOrShort := func(i int) bool {
		s := sections[i]
		if total > 0 && s.Duration()/total <= o.IntroMaxFraction {
			return true
		}
		return totalRMS > 0 && s.RMS < o.QuietRatio*totalRMS
	}

	if isQuietOrShort(0) {
		labels[0] = "Intro"
		labelled[0] = true
	}
	if n > 1 && isQuietOrShort(n-1) {
		labels[n-1] = "Outro"
		labelled[n-1] = true
	}

	chorusCluster := -1
	bestScore := 0.0
	// A song made of a single repeated part has no verse/chorus distinction
	// to infer; calling every occurrence a chorus would be noise.
	singlePart := len(meanRMS) == 1
	if !singlePart {
		for c, count := range counts {
			if count < 2 {
				continue
			}
			score := float64(count)
			if mean, ok := meanRMS[c]; ok {
				score *= 0.5 + mean
			}
			if score > bestScore {
				bestScore = score
				chorusCluster = c
			}
		}
	}

	chorusN, verseN := 0, 0
	sectionN := 0

	for i := 0; i < n; i++ {
		if labelled[i] {
			continue
		}
		c := assign[i]
		switch {
		case !singlePart && c == chorusCluster:
			chorusN++
			labels[i] = fmt.Sprintf("Chorus %d", chorusN)
		case !singlePart && counts[c] >= 2:
			verseN++
			labels[i] = fmt.Sprintf("Verse %d", verseN)
		case chorusN > 0 && i+1 < n && assign[i+1] == chorusCluster:
			labels[i] = "Bridge"
		default:
			sectionN++
			labels[i] = fmt.Sprintf("Section %d", sectionN)
		}
	}
	return labels
}

// clusterSections greedily groups sections by cosine similarity. It returns
// the cluster assignment, the cluster sizes and the mean RMS per cluster index.
func clusterSections(sections []Section, threshold float64) ([]int, map[int]int, map[int]float64) {
	type cluster struct {
		sum   []float64
		count int
		rms   float64
	}

	var clusters []cluster
	assign := make([]int, len(sections))

	for i, s := range sections {
		best, bestSim := -1, 0.0
		for ci := range clusters {
			mean := make([]float64, len(clusters[ci].sum))
			for k := range mean {
				mean[k] = clusters[ci].sum[k] / float64(clusters[ci].count)
			}
			if sim := similarity(s.Vector, mean); sim > bestSim {
				best, bestSim = ci, sim
			}
		}

		if best >= 0 && bestSim >= threshold {
			assign[i] = best
			clusters[best].count++
			clusters[best].rms += s.RMS
			for k := range clusters[best].sum {
				if k < len(s.Vector) {
					clusters[best].sum[k] += s.Vector[k]
				}
			}
			continue
		}

		sum := make([]float64, len(s.Vector))
		copy(sum, s.Vector)
		clusters = append(clusters, cluster{sum: sum, count: 1, rms: s.RMS})
		assign[i] = len(clusters) - 1
	}

	counts := make(map[int]int, len(clusters))
	means := make(map[int]float64, len(clusters))
	for i, c := range clusters {
		counts[i] = c.count
		means[i] = c.rms / float64(c.count)
	}
	return assign, counts, means
}
