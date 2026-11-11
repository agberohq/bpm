package key

import (
	"math"
	"testing"
)

func TestName(t *testing.T) {
	if got := Name(1, Minor); got != "C# minor" {
		t.Fatalf("Name(1, Minor) = %q", got)
	}
	if got := Name(-1, Major); got != "B major" {
		t.Fatalf("Name(-1, Major) = %q", got)
	}
	if got := Name(13, Major); got != "Db major" {
		t.Fatalf("Name(13, Major) = %q", got)
	}
}

func TestPearson(t *testing.T) {
	a := [12]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

	if got := Pearson(a, a); math.Abs(got-1) > 1e-12 {
		t.Fatalf("Pearson(a, a) = %v, want 1", got)
	}

	rev := [12]float64{}
	for i := range rev {
		rev[i] = a[len(a)-1-i]
	}
	if got := Pearson(a, rev); math.Abs(got+1) > 1e-12 {
		t.Fatalf("Pearson(a, reverse) = %v, want -1", got)
	}

	var flat [12]float64
	for i := range flat {
		flat[i] = 3
	}
	if got := Pearson(a, flat); got != 0 {
		t.Fatalf("Pearson with constant vector = %v, want 0", got)
	}
}

func TestCorrelateRecoversProfile(t *testing.T) {
	for _, tonic := range []int{0, 1, 6, 11} {
		for _, mode := range []Mode{Major, Minor} {
			profile := Profile(mode)

			// Rotate the profile to the tonic under test.
			var chroma Chroma
			for i := 0; i < 12; i++ {
				chroma[(tonic+i)%12] = profile[i]
			}

			got := Correlate(chroma)
			if len(got) != 24 {
				t.Fatalf("Correlate returned %d candidates, want 24", len(got))
			}
			if got[0].Key != Name(tonic, mode) {
				t.Fatalf("top key = %q, want %q", got[0].Key, Name(tonic, mode))
			}
			if math.Abs(got[0].Correlation-1) > 1e-9 {
				t.Fatalf("top correlation = %v, want 1", got[0].Correlation)
			}
			for i, c := range got {
				if c.Rank != i+1 {
					t.Fatalf("rank[%d] = %d, want %d", i, c.Rank, i+1)
				}
				if i > 0 && got[i-1].Correlation < c.Correlation {
					t.Fatalf("candidates not sorted at %d", i)
				}
			}
		}
	}
}

func TestCorrelateIsDeterministicAndComplete(t *testing.T) {
	var chroma Chroma
	for i := range chroma {
		chroma[i] = float64(i%3) + 0.1
	}

	a := Correlate(chroma)
	b := Correlate(chroma)
	seen := map[string]bool{}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("candidate %d differs between runs", i)
		}
		if seen[a[i].Key] {
			t.Fatalf("duplicate key %q", a[i].Key)
		}
		seen[a[i].Key] = true
	}
	if len(seen) != 24 {
		t.Fatalf("got %d distinct keys, want 24", len(seen))
	}
}

func TestAnalyzeStrongResult(t *testing.T) {
	profile := Profile(Minor)
	var chroma Chroma
	for i := 0; i < 12; i++ {
		chroma[(1+i)%12] = profile[i] // C# minor
	}

	c := NewCorrelator(Options{IncludeAll: true})
	got := c.Analyze(chroma, 100)

	if got.Domain != "C# minor" {
		t.Fatalf("Domain = %q, want C# minor", got.Domain)
	}
	if got.Confidence != ConfidenceStrong {
		t.Fatalf("Confidence = %q, want strong", got.Confidence)
	}
	if got.Margin < 0.1 {
		t.Fatalf("Margin = %v, want >= 0.1", got.Margin)
	}
	if len(got.Present) == 0 || got.Present[0].Key != "C# minor" {
		t.Fatalf("Present = %+v", got.Present)
	}
	if len(got.All) != 24 {
		t.Fatalf("All has %d entries, want 24", len(got.All))
	}
}

func TestAnalyzeKeepsPlausibleAlternatives(t *testing.T) {
	maj := Profile(Major)
	min := Profile(Minor)

	// A blend of C major and A minor (relative keys) must keep both.
	var chroma Chroma
	for i := 0; i < 12; i++ {
		chroma[i] = maj[i] + min[(i+3)%12] // A minor profile shifted to pc 9
	}

	c := NewCorrelator(Options{})
	got := c.Analyze(chroma, 100)

	if len(got.Present) < 2 {
		t.Fatalf("Present = %+v, want at least two candidates", got.Present)
	}
	if got.Confidence == ConfidenceStrong {
		t.Fatalf("Confidence = strong for an ambiguous blend (margin %v)", got.Margin)
	}
	first := got.Present[0].Key
	second := got.Present[1].Key
	if !(first == "C major" && second == "A minor") && !(first == "A minor" && second == "C major") {
		t.Fatalf("top two = %q, %q; want C major / A minor", first, second)
	}
}

func TestAnalyzeRequiresEnoughAudio(t *testing.T) {
	profile := Profile(Major)
	var chroma Chroma
	for i := range chroma {
		chroma[i] = profile[i]
	}

	c := NewCorrelator(Options{})
	got := c.Analyze(chroma, 1)
	if got.Domain != "" || got.Confidence != ConfidenceNone || got.Present != nil {
		t.Fatalf("short input produced a result: %+v", got)
	}
}

func TestAnalyzeSilence(t *testing.T) {
	got := NewCorrelator(Options{}).Analyze(Chroma{}, 1000)
	if got.Domain != "" || got.Confidence != ConfidenceNone {
		t.Fatalf("silence produced a key: %+v", got)
	}
}

func TestAnalyzeLowCorrelation(t *testing.T) {
	// A flat chroma has no tonal centre; correlations are all zero.
	var flat Chroma
	for i := range flat {
		flat[i] = 1
	}
	got := NewCorrelator(Options{}).Analyze(flat, 1000)
	if got.Domain != "" || got.Confidence != ConfidenceNone {
		t.Fatalf("flat chroma produced a key: %+v", got)
	}
}

func TestNoNegativeZeroMargin(t *testing.T) {
	// Identical major/minor profiles cannot happen, but equal correlations can.
	var chroma Chroma
	for i := range chroma {
		chroma[i] = 1
	}
	chroma[0] = 2
	got := NewCorrelator(Options{}).Analyze(chroma, 100)
	if got.Margin < 0 {
		t.Fatalf("Margin = %v, want >= 0", got.Margin)
	}
}
