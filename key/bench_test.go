package key

import "testing"

func benchmarkPCM(seconds float64) []float32 {
	// A C major triad with a little movement, at 44.1 kHz.
	return mix(
		sinePCM(44100, 261.63, 0.15, seconds),
		sinePCM(44100, 329.63, 0.15, seconds),
		sinePCM(44100, 392.00, 0.15, seconds),
	)
}

func BenchmarkExtract(b *testing.B) {
	pcm := benchmarkPCM(10)
	ex := NewExtractor(44100)

	b.ReportAllocs()
	b.SetBytes(int64(len(pcm) * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ex.Extract(pcm); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCorrelate(b *testing.B) {
	profile := Profile(Minor)
	var chroma Chroma
	for i := range chroma {
		chroma[i] = profile[i]
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := Correlate(chroma); len(got) != 24 {
			b.Fatalf("got %d candidates", len(got))
		}
	}
}
